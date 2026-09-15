package h2histogram

import (
	"errors"
	"fmt"
	"math"
)

// Histogram is a dense h2 histogram that stores a counter for every bucket.
//
// Values are quantized into buckets according to a Config determined by
// groupingPower and maxValuePower. This is the Go analogue of the Rust
// Histogram type and produces byte-for-byte identical bucketing, so histograms
// recorded here can be consumed by Rezolus (and vice versa).
type Histogram struct {
	config  Config
	buckets []uint64
}

// PercentileResult pairs a requested percentile with the Bucket it resolves to.
type PercentileResult struct {
	Percentile float64
	Bucket     Bucket
}

// New creates an empty histogram with the given groupingPower and
// maxValuePower. It returns an error if the parameters are invalid (see
// NewConfig).
func New(groupingPower, maxValuePower uint32) (*Histogram, error) {
	config, err := NewConfig(groupingPower, maxValuePower)
	if err != nil {
		return nil, err
	}
	return NewWithConfig(config), nil
}

// NewWithConfig creates an empty histogram from an existing Config.
// It panics for an invalid Config, including its zero value.
func NewWithConfig(config Config) *Histogram {
	if err := validateConfig(config); err != nil {
		panic(err)
	}
	return &Histogram{
		config:  config,
		buckets: make([]uint64, config.TotalBuckets()),
	}
}

// FromBuckets creates a histogram from a full, dense slice of bucket counts.
// The length of buckets must equal the config's TotalBuckets.
func FromBuckets(groupingPower, maxValuePower uint32, buckets []uint64) (*Histogram, error) {
	config, err := NewConfig(groupingPower, maxValuePower)
	if err != nil {
		return nil, err
	}
	if len(buckets) != config.TotalBuckets() {
		return nil, fmt.Errorf("h2histogram: expected %d buckets, got %d", config.TotalBuckets(), len(buckets))
	}
	h := NewWithConfig(config)
	copy(h.buckets, buckets)
	return h, nil
}

// Config returns the bucketing configuration.
func (h *Histogram) Config() Config { return h.config }

// Buckets returns the raw, dense slice of bucket counts (one entry per bucket).
// The returned slice aliases the histogram's internal storage.
func (h *Histogram) Buckets() []uint64 { return h.buckets }

// Len returns the number of buckets.
func (h *Histogram) Len() int { return len(h.buckets) }

// TotalCount returns the total number of observations recorded, modulo 2^64.
// Use CheckedTotalCount when the total may exceed uint64.
func (h *Histogram) TotalCount() uint64 {
	var total uint64
	for _, c := range h.buckets {
		total += c
	}
	return total
}

// Increment adds one observation of value. It returns an error if value is out
// of range for the histogram.
func (h *Histogram) Increment(value uint64) error {
	return h.Record(value, 1)
}

// Record adds count observations of value, with uint64 wrapping on overflow.
// It returns an error if value is out
// of range for the histogram.
func (h *Histogram) Record(value, count uint64) error {
	index, err := h.config.ValueToIndex(value)
	if err != nil {
		return err
	}
	h.buckets[index] += count
	return nil
}

// RecordMany records each value in values once. It returns an error (and stops)
// if any value is out of range.
func (h *Histogram) RecordMany(values []uint64) error {
	for _, v := range values {
		if err := h.Record(v, 1); err != nil {
			return err
		}
	}
	return nil
}

// RecordManyWithCounts records each value with the corresponding weight from
// counts. It returns an error if the slices differ in length or any value is
// out of range.
func (h *Histogram) RecordManyWithCounts(values, counts []uint64) error {
	if len(values) != len(counts) {
		return fmt.Errorf("h2histogram: values and counts must have the same length (%d != %d)", len(values), len(counts))
	}
	for i, v := range values {
		if err := h.Record(v, counts[i]); err != nil {
			return err
		}
	}
	return nil
}

// Buckets iteration -------------------------------------------------------

// EachBucket calls fn for every bucket in ascending index order.
func (h *Histogram) EachBucket(fn func(Bucket)) {
	for index, count := range h.buckets {
		start, end := h.config.IndexToRange(index)
		fn(Bucket{Count: count, Start: start, End: end})
	}
}

// NonzeroBuckets returns every bucket with a non-zero count, in ascending index
// order.
func (h *Histogram) NonzeroBuckets() []Bucket {
	var out []Bucket
	for index, count := range h.buckets {
		if count != 0 {
			start, end := h.config.IndexToRange(index)
			out = append(out, Bucket{Count: count, Start: start, End: end})
		}
	}
	return out
}

// Combination -------------------------------------------------------------

func (h *Histogram) checkCompatible(other *Histogram) error {
	if other == nil || h.config != other.config {
		return errors.New("h2histogram: histograms have incompatible configurations")
	}
	return nil
}

// Merge returns a new histogram that is the element-wise sum of h and other.
// Both histograms must share the same configuration. Counts wrap modulo 2^64.
// Use CheckedSum to reject bucket overflow.
func (h *Histogram) Merge(other *Histogram) (*Histogram, error) {
	if err := h.checkCompatible(other); err != nil {
		return nil, err
	}
	result := NewWithConfig(h.config)
	for i, n := range h.buckets {
		result.buckets[i] = n + other.buckets[i]
	}
	return result, nil
}

// Subtract returns a new histogram that is the element-wise difference of h and
// other. It returns an error if any bucket would go negative or the configs
// differ.
func (h *Histogram) Subtract(other *Histogram) (*Histogram, error) {
	if err := h.checkCompatible(other); err != nil {
		return nil, err
	}
	result := NewWithConfig(h.config)
	for i := range h.buckets {
		if other.buckets[i] > h.buckets[i] {
			return nil, errors.New("h2histogram: subtraction would produce a negative bucket count")
		}
		result.buckets[i] = h.buckets[i] - other.buckets[i]
	}
	return result, nil
}

// Downsample returns a coarser histogram with a smaller groupingPower. Every
// step down approximately halves the number of buckets while doubling the
// relative error. The new grouping power must be strictly less than the current
// one. Coalesced counts wrap modulo 2^64; use CheckedDownsample to reject overflow.
func (h *Histogram) Downsample(groupingPower uint32) (*Histogram, error) {
	return h.downsample(groupingPower, false)
}

// CheckedDownsample produces coarser geometry, rejecting per-bucket overflow.
func (h *Histogram) CheckedDownsample(groupingPower uint32) (*Histogram, error) {
	return h.downsample(groupingPower, true)
}

func (h *Histogram) downsample(groupingPower uint32, checked bool) (*Histogram, error) {
	if groupingPower >= h.config.groupingPower {
		return nil, errors.New("h2histogram: target grouping_power must be less than the current grouping_power")
	}
	result, err := New(groupingPower, h.config.maxValuePower)
	if err != nil {
		return nil, err
	}
	for index, count := range h.buckets {
		if count != 0 {
			value := h.config.IndexToLowerBound(index)
			target, err := result.config.ValueToIndex(value)
			if err != nil {
				return nil, err
			}
			if checked {
				if err := checkAdd(result.buckets[target], count); err != nil {
					return nil, err
				}
			}
			result.buckets[target] += count
		}
	}
	return result, nil
}

// Quantiles / percentiles -------------------------------------------------

// Percentile returns the bucket at a single percentile in [0.0, 1.0]. It
// returns a nil bucket if the histogram is empty, and an error if the
// percentile is out of range. percentile uses the same fractional convention as
// the Rust crate: 0.5 is the median.
func (h *Histogram) Percentile(percentile float64) (*Bucket, error) {
	if err := validatePercentile(percentile); err != nil {
		return nil, err
	}
	total, err := h.CheckedTotalCount()
	if err != nil || total == 0 {
		return nil, err
	}
	b := scanBucket(h.config, nil, h.buckets, ceilCount(percentile, total))
	return &b, nil
}

// Percentiles returns results in input order, including duplicate requests.
func (h *Histogram) Percentiles(percentiles []float64) ([]PercentileResult, error) {
	return sortedPercentiles(h.config, nil, h.buckets, percentiles)
}

// Quantile is an alias for Percentile (the crate uses "quantile").
func (h *Histogram) Quantile(quantile float64) (*Bucket, error) {
	return h.Percentile(quantile)
}

// Conversions -------------------------------------------------------------

// ToSparse converts to the sparse (columnar) representation.
func (h *Histogram) ToSparse() *SparseHistogram {
	return SparseFromHistogram(h)
}

// ToCumulative converts to a read-only cumulative histogram for fast quantiles.
// It panics on total overflow; use CheckedToCumulative for an error instead.
func (h *Histogram) ToCumulative() *CumulativeHistogram {
	return CumulativeFromHistogram(h)
}

// Equal reports whether h and other have the same configuration and bucket
// counts.
func (h *Histogram) Equal(other *Histogram) bool {
	if h.config != other.config || len(h.buckets) != len(other.buckets) {
		return false
	}
	for i := range h.buckets {
		if h.buckets[i] != other.buckets[i] {
			return false
		}
	}
	return true
}

// String implements fmt.Stringer.
func (h *Histogram) String() string {
	return fmt.Sprintf("Histogram(grouping_power=%d, max_value_power=%d, total_count=%d)",
		h.config.groupingPower, h.config.maxValuePower, h.TotalCount())
}

// ceilCount computes max(1, ceil(p*total)) matching the Rust crate's
// max(1, (q*total).ceil() as u128).
func ceilCount(p float64, total uint64) uint64 {
	if p >= 1 {
		return total
	}
	value := math.Ceil(p * float64(total))
	// float64 rounds MaxUint64 to 2^64; never convert that out-of-range value.
	if value >= float64(total) {
		return total
	}
	if value < 1 {
		return 1
	}
	return uint64(value)
}
