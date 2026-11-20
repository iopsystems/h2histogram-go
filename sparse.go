package h2histogram

import "fmt"

// SparseHistogram is a histogram stored as (index, count) pairs for non-zero
// buckets.
//
// Only non-zero buckets are stored, as two parallel slices Index and Count in
// ascending index order. This is the form Rezolus uses for its
// :bucket_indices / :bucket_counts parquet columns.
type SparseHistogram struct {
	config Config
	index  []int
	count  []uint64
}

// SparseFromHistogram builds a sparse histogram from a dense Histogram.
func SparseFromHistogram(h *Histogram) *SparseHistogram {
	var index []int
	var count []uint64
	for i, c := range h.buckets {
		if c != 0 {
			index = append(index, i)
			count = append(count, c)
		}
	}
	return &SparseHistogram{config: h.config, index: index, count: count}
}

// SparseFromParts creates a sparse histogram from raw parts, validating
// invariants. It returns an error if the lengths differ, an index is out of
// range, or the indices are not strictly ascending. Zero counts are omitted.
func SparseFromParts(config Config, index []int, count []uint64) (*SparseHistogram, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if len(index) != len(count) {
		return nil, fmt.Errorf("h2histogram: index and count must have the same length (%d != %d)", len(index), len(count))
	}
	total := config.TotalBuckets()
	prev := -1
	for _, i := range index {
		if i < 0 || i >= total {
			return nil, fmt.Errorf("h2histogram: index %d out of range for config", i)
		}
		if i <= prev {
			return nil, fmt.Errorf("h2histogram: indices must be strictly ascending")
		}
		prev = i
	}
	idx := make([]int, len(index))
	copy(idx, index)
	cnt := make([]uint64, len(count))
	copy(cnt, count)
	out := &SparseHistogram{config: config}
	for k, n := range cnt {
		if n != 0 {
			out.index = append(out.index, idx[k])
			out.count = append(out.count, n)
		}
	}
	return out, nil
}

// Config returns the bucketing configuration.
func (s *SparseHistogram) Config() Config { return s.config }

// Index returns a copy of the non-zero bucket indices, ascending.
func (s *SparseHistogram) Index() []int { return append([]int(nil), s.index...) }

// Count returns a copy of the counts corresponding to Index.
func (s *SparseHistogram) Count() []uint64 { return append([]uint64(nil), s.count...) }

// Len returns the number of stored (non-zero) buckets.
func (s *SparseHistogram) Len() int { return len(s.index) }

// IsEmpty reports whether the histogram has no stored buckets.
func (s *SparseHistogram) IsEmpty() bool { return len(s.index) == 0 }

// TotalCount returns the total modulo 2^64. Use CheckedTotalCount to reject overflow.
func (s *SparseHistogram) TotalCount() uint64 {
	var total uint64
	for _, c := range s.count {
		total += c
	}
	return total
}

// Buckets returns each stored bucket with its individual count.
func (s *SparseHistogram) Buckets() []Bucket {
	out := make([]Bucket, len(s.index))
	for k, i := range s.index {
		start, end := s.config.IndexToRange(i)
		out[k] = Bucket{Count: s.count[k], Start: start, End: end}
	}
	return out
}

// ToDense converts to a dense Histogram.
// It panics if the configuration is invalid, including for a zero-value snapshot.
func (s *SparseHistogram) ToDense() *Histogram {
	h := NewWithConfig(s.config)
	for k, i := range s.index {
		h.buckets[i] = s.count[k]
	}
	return h
}

// ToCumulative converts to a read-only CumulativeHistogram.
// It panics on total overflow; use CheckedToCumulative for an error instead.
func (s *SparseHistogram) ToCumulative() *CumulativeHistogram {
	return CumulativeFromSparse(s)
}

// Percentile scans the stored buckets directly.
func (s *SparseHistogram) Percentile(percentile float64) (*Bucket, error) {
	if err := validatePercentile(percentile); err != nil {
		return nil, err
	}
	total, err := s.CheckedTotalCount()
	if err != nil || total == 0 {
		return nil, err
	}
	b := scanBucket(s.config, s.index, s.count, ceilCount(percentile, total))
	return &b, nil
}

// Percentiles reports results in request order.
func (s *SparseHistogram) Percentiles(percentiles []float64) ([]PercentileResult, error) {
	return sortedPercentiles(s.config, s.index, s.count, percentiles)
}

// Equal reports whether s and other have the same configuration, indices and
// counts.
func (s *SparseHistogram) Equal(other *SparseHistogram) bool {
	if s.config != other.config || len(s.index) != len(other.index) {
		return false
	}
	for i := range s.index {
		if s.index[i] != other.index[i] || s.count[i] != other.count[i] {
			return false
		}
	}
	return true
}

// String implements fmt.Stringer.
func (s *SparseHistogram) String() string {
	return fmt.Sprintf("SparseHistogram(grouping_power=%d, max_value_power=%d, nonzero_buckets=%d)",
		s.config.groupingPower, s.config.maxValuePower, len(s.index))
}
