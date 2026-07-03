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

## Compatibility across implementations

The same bucketing is implemented in:

- [Rust](https://github.com/iopsystems/histogram) — the canonical implementation
- [Python](https://github.com/iopsystems/h2histogram-py)
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

## License

MIT — see [LICENSE](LICENSE).
