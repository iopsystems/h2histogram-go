package h2histogram

import (
	"fmt"
	"sort"
)

// CumulativeHistogram is a read-only histogram with cumulative counts for fast
// quantile queries.
//
// It corresponds to CumulativeROHistogram in the Rust histogram crate. It is a
// variant of SparseHistogram that stores only non-zero buckets in columnar
// form, but with cumulative counts: count[i] is the running prefix sum of
// individual bucket counts, so the last element equals the total observation
// count.
//
// Because the counts are cumulative, percentile queries are answered with a
// binary search (O(log n) in the number of non-zero buckets) rather than a
// linear scan. The histogram is read-only. A midpoint-estimated mean is
// computed once at construction.
type CumulativeHistogram struct {
	config  Config
	index   []int
	count   []uint64 // cumulative (prefix-sum) counts
	mean    float64
	hasMean bool
}

// CumulativeFromParts creates a CumulativeHistogram from raw parts. count must
// be cumulative (prefix sums). It returns an error if the lengths differ, an
// index is out of range, the indices are not strictly ascending, the counts are
// not non-decreasing, or any count is zero.
func CumulativeFromParts(config Config, index []int, count []uint64) (*CumulativeHistogram, error) {
	if err := validateCumulative(config, index, count); err != nil {
		return nil, err
	}
	idx := make([]int, len(index))
	copy(idx, index)
	cnt := make([]uint64, len(count))
	copy(cnt, count)
	return newCumulative(config, idx, cnt), nil
}

// CumulativeFromHistogram builds a CumulativeHistogram from a dense Histogram.
func CumulativeFromHistogram(h *Histogram) *CumulativeHistogram {
	var index []int
	var count []uint64
	var running uint64
	for i, n := range h.buckets {
		if n != 0 {
			running += n
			index = append(index, i)
			count = append(count, running)
		}
	}
	return newCumulative(h.config, index, count)
}

// CumulativeFromSparse builds a CumulativeHistogram from a SparseHistogram.
func CumulativeFromSparse(s *SparseHistogram) *CumulativeHistogram {
	index := make([]int, len(s.index))
	copy(index, s.index)
	cumulative := make([]uint64, len(s.count))
	var running uint64
	for i, n := range s.count {
		running += n
		cumulative[i] = running
	}
	return newCumulative(s.config, index, cumulative)
}

func newCumulative(config Config, index []int, count []uint64) *CumulativeHistogram {
	c := &CumulativeHistogram{config: config, index: index, count: count}
	c.mean, c.hasMean = c.computeMean()
	return c
}

func validateCumulative(config Config, index []int, count []uint64) error {
	if len(index) != len(count) {
		return fmt.Errorf("h2histogram: index and count must have the same length (%d != %d)", len(index), len(count))
	}
	total := config.TotalBuckets()
	prev := -1
	for _, i := range index {
		if i < 0 || i >= total {
			return fmt.Errorf("h2histogram: index %d out of range for config", i)
		}
		if i <= prev {
			return fmt.Errorf("h2histogram: indices must be strictly ascending")
		}
		prev = i
	}
	var prevC uint64
	havePrev := false
	for _, c := range count {
		if c == 0 {
			return fmt.Errorf("h2histogram: cumulative counts must be non-zero")
		}
		if havePrev && c < prevC {
			return fmt.Errorf("h2histogram: cumulative counts must be non-decreasing")
		}
		prevC = c
		havePrev = true
	}
	return nil
}

func (c *CumulativeHistogram) individualCount(position int) uint64 {
	if position == 0 {
		return c.count[0]
	}
	return c.count[position] - c.count[position-1]
}

func (c *CumulativeHistogram) computeMean() (float64, bool) {
	if len(c.count) == 0 {
		return 0, false
	}
	total := c.count[len(c.count)-1]
	if total == 0 {
		return 0, false
	}
	var weighted float64
	for i := range c.index {
		individual := c.individualCount(i)
		start, end := c.config.IndexToRange(c.index[i])
		midpoint := (float64(start) + float64(end)) / 2.0
		weighted += midpoint * float64(individual)
	}
	return weighted / float64(total), true
}

// Config returns the bucketing configuration.
func (c *CumulativeHistogram) Config() Config { return c.config }

// Index returns the non-zero bucket indices, ascending.
func (c *CumulativeHistogram) Index() []int { return c.index }

// Count returns the cumulative (prefix-sum) counts aligned with Index.
func (c *CumulativeHistogram) Count() []uint64 { return c.count }

// Len returns the number of stored (non-zero) buckets.
func (c *CumulativeHistogram) Len() int { return len(c.index) }

// IsEmpty reports whether the histogram has no stored buckets.
func (c *CumulativeHistogram) IsEmpty() bool { return len(c.index) == 0 }

// TotalCount returns the total number of observations.
func (c *CumulativeHistogram) TotalCount() uint64 {
	if len(c.count) == 0 {
		return 0
	}
	return c.count[len(c.count)-1]
}

// Mean returns the midpoint-estimated mean of all observations, and false if
// the histogram is empty. It is computed once at construction.
func (c *CumulativeHistogram) Mean() (float64, bool) {
	return c.mean, c.hasMean
}

// findQuantilePosition returns the first position where the cumulative count is
// >= target.
func (c *CumulativeHistogram) findQuantilePosition(target uint64) int {
	pos := sort.Search(len(c.count), func(i int) bool {
		return c.count[i] >= target
	})
	if pos >= len(c.count) {
		return len(c.count) - 1
	}
	return pos
}

// Percentile returns the Bucket at percentile in [0.0, 1.0]. The returned
// bucket carries the individual (non-cumulative) count. It returns a nil bucket
// if the histogram is empty.
func (c *CumulativeHistogram) Percentile(percentile float64) (*Bucket, error) {
	results, err := c.Percentiles([]float64{percentile})
	if err != nil {
		return nil, err
	}
	if results == nil {
		return nil, nil
	}
	b := results[0].Bucket
	return &b, nil
}

// Percentiles returns a PercentileResult per requested percentile, in input
// order. Each percentile must be in [0.0, 1.0]. It returns a nil slice if
// empty.
func (c *CumulativeHistogram) Percentiles(percentiles []float64) ([]PercentileResult, error) {
	for _, p := range percentiles {
		if p < 0.0 || p > 1.0 {
			return nil, fmt.Errorf("h2histogram: percentiles must be in the range [0.0, 1.0], got %v", p)
		}
	}
	if len(c.count) == 0 {
		return nil, nil
	}
	total := c.count[len(c.count)-1]
	if total == 0 {
		return nil, nil
	}

	out := make([]PercentileResult, len(percentiles))
	for i, p := range percentiles {
		target := ceilCount(p, total)
		pos := c.findQuantilePosition(target)
		start, end := c.config.IndexToRange(c.index[pos])
		out[i] = PercentileResult{
			Percentile: p,
			Bucket:     Bucket{Count: c.individualCount(pos), Start: start, End: end},
		}
	}
	return out, nil
}

// Quantile is an alias for Percentile.
func (c *CumulativeHistogram) Quantile(quantile float64) (*Bucket, error) {
	return c.Percentile(quantile)
}

// BucketQuantileRange returns the (lower, upper) quantile fractions for the
// bucketIdx-th stored bucket. lower is the fraction of observations strictly
// before this bucket and upper the fraction at or before it, both in
// [0.0, 1.0]. The second return value is false if empty or out of range.
func (c *CumulativeHistogram) BucketQuantileRange(bucketIdx int) (float64, float64, bool) {
	if bucketIdx < 0 || bucketIdx >= len(c.count) {
		return 0, 0, false
	}
	total := c.count[len(c.count)-1]
	if total == 0 {
		return 0, 0, false
	}
	var lower float64
	if bucketIdx != 0 {
		lower = float64(c.count[bucketIdx-1]) / float64(total)
	}
	upper := float64(c.count[bucketIdx]) / float64(total)
	return lower, upper, true
}

// Buckets returns each stored bucket with its individual count.
func (c *CumulativeHistogram) Buckets() []Bucket {
	out := make([]Bucket, len(c.index))
	for i := range c.index {
		start, end := c.config.IndexToRange(c.index[i])
		out[i] = Bucket{Count: c.individualCount(i), Start: start, End: end}
	}
	return out
}

// BucketWithQuantiles bundles a bucket with its quantile span.
type BucketWithQuantiles struct {
	Bucket        Bucket
	LowerQuantile float64
	UpperQuantile float64
}

// BucketsWithQuantiles returns each non-zero bucket with its
// (lowerQuantile, upperQuantile) span.
func (c *CumulativeHistogram) BucketsWithQuantiles() []BucketWithQuantiles {
	out := make([]BucketWithQuantiles, len(c.index))
	var total uint64
	if len(c.count) != 0 {
		total = c.count[len(c.count)-1]
	}
	for i := range c.index {
		var lower float64
		if i != 0 {
			lower = float64(c.count[i-1]) / float64(total)
		}
		upper := float64(c.count[i]) / float64(total)
		start, end := c.config.IndexToRange(c.index[i])
		out[i] = BucketWithQuantiles{
			Bucket:        Bucket{Count: c.individualCount(i), Start: start, End: end},
			LowerQuantile: lower,
			UpperQuantile: upper,
		}
	}
	return out
}

// ToDense reconstructs a dense Histogram.
func (c *CumulativeHistogram) ToDense() *Histogram {
	h := NewWithConfig(c.config)
	for i := range c.index {
		h.buckets[c.index[i]] = c.individualCount(i)
	}
	return h
}

// Equal reports whether c and other have the same configuration, indices and
// cumulative counts.
func (c *CumulativeHistogram) Equal(other *CumulativeHistogram) bool {
	if c.config != other.config || len(c.index) != len(other.index) {
		return false
	}
	for i := range c.index {
		if c.index[i] != other.index[i] || c.count[i] != other.count[i] {
			return false
		}
	}
	return true
}

// String implements fmt.Stringer.
func (c *CumulativeHistogram) String() string {
	return fmt.Sprintf("CumulativeHistogram(grouping_power=%d, max_value_power=%d, nonzero_buckets=%d, total_count=%d)",
		c.config.groupingPower, c.config.maxValuePower, len(c.index), c.TotalCount())
}
