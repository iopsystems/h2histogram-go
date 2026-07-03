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
func NewWithConfig(config Config) *Histogram {
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

// TotalCount returns the total number of observations recorded.
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

// Record adds count observations of value. It returns an error if value is out
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
	if h.config != other.config {
		return errors.New("h2histogram: histograms have incompatible configurations")
	}
	return nil
}

// Merge returns a new histogram that is the element-wise sum of h and other.
// Both histograms must share the same configuration.
func (h *Histogram) Merge(other *Histogram) (*Histogram, error) {
	if err := h.checkCompatible(other); err != nil {
		return nil, err
	}
	result := NewWithConfig(h.config)
	for i := range h.buckets {
		result.buckets[i] = h.buckets[i] + other.buckets[i]
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
// one.
func (h *Histogram) Downsample(groupingPower uint32) (*Histogram, error) {
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
			if err := result.Record(value, count); err != nil {
				return nil, err
			}
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
	results, err := h.Percentiles([]float64{percentile})
	if err != nil {
		return nil, err
	}
	if results == nil {
		return nil, nil
	}
	b := results[0].Bucket
	return &b, nil
}

// Percentiles returns a PercentileResult for each requested percentile, in the
// same order as the input. Each percentile must be in [0.0, 1.0]. It returns a
// nil slice if the histogram is empty. This mirrors the algorithm used by the
// Rust crate.
func (h *Histogram) Percentiles(percentiles []float64) ([]PercentileResult, error) {
	for _, p := range percentiles {
		if p < 0.0 || p > 1.0 {
			return nil, fmt.Errorf("h2histogram: percentiles must be in the range [0.0, 1.0], got %v", p)
		}
	}

	total := h.TotalCount()
	if total == 0 {
		return nil, nil
	}

	// Deduplicate and sort while remembering the original order for output.
	sortedUnique := sortedUniqueFloats(percentiles)

	resultsByP := make(map[float64]Bucket, len(sortedUnique))
	bucketIdx := 0
	partialSum := h.buckets[0]

	for _, p := range sortedUnique {
		target := ceilCount(p, total)
		for {
			if partialSum >= target {
				start, end := h.config.IndexToRange(bucketIdx)
				resultsByP[p] = Bucket{Count: h.buckets[bucketIdx], Start: start, End: end}
				break
			}
			if bucketIdx == len(h.buckets)-1 {
				start, end := h.config.IndexToRange(bucketIdx)
				resultsByP[p] = Bucket{Count: h.buckets[bucketIdx], Start: start, End: end}
				break
			}
			bucketIdx++
			partialSum += h.buckets[bucketIdx]
		}
	}

	out := make([]PercentileResult, len(percentiles))
	for i, p := range percentiles {
		out[i] = PercentileResult{Percentile: p, Bucket: resultsByP[p]}
	}
	return out, nil
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
	target := uint64(math.Ceil(p * float64(total)))
	if target < 1 {
		return 1
	}
	return target
}

// sortedUniqueFloats returns the sorted, de-duplicated set of the inputs.
func sortedUniqueFloats(values []float64) []float64 {
	seen := make(map[float64]struct{}, len(values))
	var out []float64
	for _, v := range values {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	// Simple insertion sort keeps this dependency-free; the number of distinct
	// percentiles is always tiny.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
