# Changelog

## Unreleased

### Breaking behavior changes from v0.1.0

- Dense/sparse `ToCumulative`, `CumulativeFromHistogram`, and
  `CumulativeFromSparse` now panic if total counts exceed `math.MaxUint64`.
  Previously they constructed wrapped, invalid prefixes. Use
  `CheckedToCumulative` and handle its error for imported or untrusted data.
  This intentionally differs from Rust histogram v1.6.0's wrapping conversion.
- Dense/sparse percentile reports now return an error when the total exceeds
  `math.MaxUint64` (empty request batches still return immediately). Rust
  v1.6.0 widens these totals to `u128` and can answer such queries. Go rank
  calculation uses floating-point multiplication and clamps endpoints to the
  valid total; it does not promise exact integer or `u128` rank arithmetic.
- `NewWithConfig` now panics for invalid configurations, including `Config{}`.
  Sparse/cumulative `ToDense` inherits this panic for zero-value snapshots.
  Use validated constructors to create empty histograms. Dense `Merge` and
  `Subtract` return errors for invalid configurations, including two zero-value
  inputs that previously produced an empty result. Zero-value dense
  `Downsample` continues to return an error.
- `SparseFromParts` omits explicit zero-count entries. Cumulative imports still
  accept repeated prefixes, but `ToSparse` omits their zero individual deltas.
  Imported Rezolus columns preserve their observations, but a round trip may
  change stored lengths and structural equality; retain original columns if
  their exact shape matters.
- Sparse/cumulative `Index()` and `Count()` return defensive copies. Mutating
  these slices no longer changes a snapshot. Dense `Buckets()` remains a
  mutable alias.

### Added

- Reusable reporting, checked aggregation/conversion, and native
  sparse/cumulative analytics. New sparse/cumulative `Merge` and `Downsample`
  reject per-bucket overflow; cumulative outputs also reject total overflow.
  Sparse downsampling is intentionally stricter than Rust v1.6.0, which wraps.
  Legacy dense `Merge` and `Downsample` retain wrapping arithmetic; use
  `CheckedSum` and `CheckedDownsample` for checked alternatives.

These compatibility changes require an explicit release note and version
decision before release; this entry does not select or publish a version.
