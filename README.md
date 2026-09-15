# h2histogram-go

[![CI](https://github.com/iopsystems/h2histogram-go/actions/workflows/ci.yml/badge.svg)](https://github.com/iopsystems/h2histogram-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/iopsystems/h2histogram-go.svg)](https://pkg.go.dev/github.com/iopsystems/h2histogram-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A pure-Go implementation of the [iopsystems h2 histogram](https://github.com/iopsystems/histogram).

`h2histogram` produces histograms with **byte-for-byte identical bucketing** to the
Rust `histogram` crate, so histograms recorded here can be consumed by
[Rezolus](https://github.com/iopsystems/rezolus) — and, conversely, you can read an
h2histogram produced by Rezolus (or the [Python](https://github.com/iopsystems/h2histogram-py)
implementation) and analyze it in Go. Go's native `uint64` means the full `u64`
value range is supported, exactly like the Rust crate.

## What is an h2 histogram?

An h2 histogram quantizes values into buckets using two parameters:

- **`groupingPower`** — the number of buckets spanning each power of two. It sets
  the relative error to `2^-groupingPower` (e.g. `groupingPower=7` → ~0.78% error).
- **`maxValuePower`** — the largest representable value is `2^maxValuePower - 1`.

Values below `2^(groupingPower+1)` are stored **exactly** (linear buckets of width 1);
larger values fall into logarithmic buckets. This gives HDR-histogram-like guarantees
with a simpler, faster bucket index computation. Rezolus records histograms with
`groupingPower=3` and `maxValuePower=64`.

## Install

```bash
go get github.com/iopsystems/h2histogram-go
```

```go
import h2histogram "github.com/iopsystems/h2histogram-go"
```

## Quick start

```go
package main

import (
	"fmt"

	h2histogram "github.com/iopsystems/h2histogram-go"
)

func main() {
	h, err := h2histogram.New(7, 64) // groupingPower, maxValuePower
	if err != nil {
		panic(err)
	}

	h.Increment(42)
	h.Record(1000, 5)                   // value, count
	h.RecordMany([]uint64{12, 15, 900}) // bulk

	fmt.Println(h.TotalCount()) // 7

	p99, _ := h.Percentile(0.99) // *Bucket (nil if empty)
	lo, hi := p99.Range()
	fmt.Println(lo, hi, p99.Midpoint())

	// Combine / reduce
	coarse, _ := h.Downsample(4) // fewer buckets, higher error, same total count
	sparse := h.ToSparse()       // columnar (index, count) form for storage
	_ = coarse
	_ = sparse
}
```

### Fast repeated quantile queries

For a snapshot you'll query many times, convert to a `CumulativeHistogram`
(the crate's `CumulativeROHistogram`). It stores non-zero buckets with
**cumulative** counts, so percentiles are answered with a binary search, and it
precomputes a midpoint-estimated mean:

```go
c := h.ToCumulative()                  // read-only; also SparseHistogram.ToCumulative()
b, _ := c.Percentile(0.99)             // O(log n) binary search -> *Bucket (individual count)
mean, ok := c.Mean()                   // midpoint-estimated mean, computed once
lo, hi, ok := c.BucketQuantileRange(0) // quantile fractions of a stored bucket
for _, bq := range c.BucketsWithQuantiles() {
	_ = bq // each non-zero bucket with its quantile span
}
```

## API overview

| Type | Purpose |
|------|---------|
| `Config` | Bucketing parameters; `ValueToIndex`, `IndexToRange`, `TotalBuckets`, `Error` |
| `Histogram` | Dense histogram; `Increment`, `Record`, `RecordMany`, `Percentile(s)`, `Merge`, `Subtract`, `Downsample`, `ToSparse`, `ToCumulative`, `FromBuckets` |
| `SparseHistogram` | Columnar `(index, count)` form; `SparseFromHistogram`, `SparseFromParts`, `ToDense`, `ToCumulative` |
| `CumulativeHistogram` | Read-only cumulative form (crate's `CumulativeROHistogram`); binary-search `Percentile(s)`, `Mean`, `BucketQuantileRange`, `BucketsWithQuantiles` |
| `Bucket` | A bucket's `Count` and inclusive `[Start, End]` range, plus `Midpoint`/`Width` |

## Recording, reporting, and analytics phases

Keep a dense `Histogram` for recording, then copy or drain it into compatible
storage at a reporting boundary. `Reset` retains the dense slice allocation.
`SnapshotInto(dst)` overwrites the destination; `DrainInto(dst)` overwrites it
and clears the source. Incompatible configurations fail before mutation;
self-snapshot is a no-op and self-drain returns an error, including a destination
created by shallow-copying the source histogram.

```go
snapshot := h2histogram.NewWithConfig(h.Config())
if err := h.DrainInto(snapshot); err != nil { panic(err) }
report, err := snapshot.CheckedToCumulative()
if err != nil { panic(err) }
requests := []float64{0.99, 0.5, 1, 0.5}
out := make([]h2histogram.PercentileResult, 0, len(requests))
out, err = report.PercentilesInto(requests, out)
```

`CheckedAddAssign(other)` validates every bucket before changing the receiver,
including self-addition. `CheckedSum([]*Histogram{a, a, b})` validates every
configuration first and returns independent owned storage. Empty input is an
error; a singleton is an independent copy. The private sum output copies the
first input, then checks and adds each remaining input in one pass. A zero-value
`Histogram{}` is rejected with an error. Existing dense `Merge` retains wrapping
arithmetic; use `CheckedSum` when overflow must be rejected.

`Percentile` uses a direct scan for dense/sparse data and binary search for
cumulative snapshots. `PercentilesInto(requests, dst)` is available on all three
types and reuses the supplied result slice when capacity is sufficient. Request
order and duplicates are preserved, NaN/infinity are rejected, and an empty
histogram returns nil. Empty request batches return immediately, even when total
counts would overflow. Allocating dense/sparse `Percentiles` sorts request positions
and scans storage once: O(buckets + requests × log(requests)). Reused dense/sparse
`PercentilesInto` computes the total once, then scans per request:
O(buckets × requests), without sorting scratch or maps.
Use a cumulative snapshot for large or repeated query sets. Scalar APIs return
`*Bucket`, which may allocate; reused batch reports allocate no result storage.

`SparseHistogram.Merge` and `Downsample` process sorted entries directly.
`CumulativeHistogram.ToSparse`, `Merge`, and `Downsample` operate through sparse
individual counts without constructing dense arrays. These cumulative analytics
use temporary sparse arrays and recompute prefixes and the midpoint mean for the
output geometry. `Compact()` on sparse/cumulative values optionally copies slices
to release spare capacity; it may allocate. Dense storage already has exact size.

### Count bounds, imports, and concurrency

Counts remain `uint64`. Recording and the legacy dense/sparse `TotalCount` retain
modulo-2^64 arithmetic; callers must avoid overflowing a recorded bucket.
`CheckedTotalCount` detects total overflow on dense/sparse values. Checked
aggregation and new sparse/cumulative `Merge` and `Downsample` reject per-bucket
overflow. Existing dense `Merge` and `Downsample` retain modulo-2^64 arithmetic;
use `CheckedSum` and dense `CheckedDownsample` for checked alternatives. Dense/sparse
storage can represent a total larger than `uint64`, but percentile reports and
cumulative conversion reject it. `CheckedToCumulative` returns an error;
existing `ToCumulative`, `CumulativeFromHistogram`, and `CumulativeFromSparse`
signatures instead panic on overflow, so wrapped prefixes are never constructed.
Percentile ranks use floating-point multiplication, with endpoints clamped to
the valid uint64 total; interior ranks retain floating-point rounding.

Constructors validate geometry and imported slice shapes/order/ranges.
`NewWithConfig` panics on invalid configuration (including `Config{}`), and
`NewConfig` rejects geometry that cannot fit its bucket-index representation.
Sparse imports omit zero counts. Cumulative imports retain compatibility with
non-decreasing prefixes; `ToSparse` omits their zero individual deltas.
Snapshot `Index()` and `Count()` getters return independent copies to protect
invariants and cached means. Dense `Buckets()` remains a mutable alias.

Dense recording, snapshots, drains, and in-place addition are **not concurrent**:
callers must synchronize shared access. Read-only snapshots can be shared after
safe publication, provided `Compact` is not run concurrently. No atomic recorder,
u32 count family, forced SIMD, assembly, or recording metadata cache is added.

### Reproducible phase benchmarks

```bash
go test ./...
go test -race ./...
go vet ./...
go test -run '^$' -bench BenchmarkReportingPhases -benchmem -count 5
```

The benchmarks prepare inputs outside timed phases and separate recording,
reset, reused snapshot/drain, owned aggregation, scalar/batch reporting,
conversion, and sparse/cumulative analytics. The drain phase includes a snapshot
refill so each drain contains data. Owned phases include result construction and
normal garbage-collection pressure; reused phases retain output capacity. Scalar
results escape to a sink. Timing and allocation measurements depend on the Go
version and machine; no Rust speedup is assumed to apply to this port.

## Compatibility across implementations

The same bucketing is implemented in:

- [Rust](https://github.com/iopsystems/histogram) — the canonical implementation
- [Python](https://github.com/iopsystems/h2histogram-py)
- [Java](https://github.com/iopsystems/h2histogram-java) — full `u64` range via
  unsigned `long` semantics
- [JavaScript](https://github.com/iopsystems/h2histogram-js) (limited to `maxValuePower <= 53`,
  since JS numbers are 64-bit floats)
- Go (this repository) — full `u64` range

Because the bucket indices are identical, a `(bucket_indices, bucket_counts)`
pair produced by any of these can be loaded via `SparseFromParts` /
`CumulativeFromParts` and analyzed here.

## Correctness

The bucketing math is verified against the exact assertions from the Rust crate's
own unit tests (`src/config.rs`), so the bucketing is guaranteed bit-identical.
Run `go test ./...` to see for yourself.

## Releasing

Go has no package registry to upload to — a module **is** its Git repository, and
"publishing" a version means pushing a [semver](https://semver.org/) tag. The
[module proxy](https://proxy.golang.org) fetches and caches it the first time
anyone requests that version.

1. **Land your changes on `main`** via a pull request, and make sure the module
   path in [`go.mod`](go.mod) matches the repository
   (`github.com/iopsystems/h2histogram-go`).
2. **Tag and push** a `vX.Y.Z` tag on `main`:

   ```bash
   git checkout main && git pull
   git tag v0.1.0        # leading "v" is required
   git push origin v0.1.0
   ```

   That is the entire release. Users can now:

   ```bash
   go get github.com/iopsystems/h2histogram-go@v0.1.0
   ```
3. **(Optional) prime the proxy and docs** so the version shows up on
   [pkg.go.dev](https://pkg.go.dev):

   ```bash
   GOPROXY=proxy.golang.org go list -m github.com/iopsystems/h2histogram-go@v0.1.0
   ```

> **Tags are effectively immutable.** The proxy caches by version, so never move
> or delete a published tag — cut a new one instead. Run `go vet ./...` and
> `go test ./...` before tagging.
>
> **`v2` and beyond** require a `/v2` suffix on the module path (per the
> [Go module version rules](https://go.dev/blog/v2-go-modules)); `v0`/`v1` need
> no suffix.

## License

MIT — see [LICENSE](LICENSE).
