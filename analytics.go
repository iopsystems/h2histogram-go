package h2histogram

import (
	"errors"
	"fmt"
	"math"
)

func validateConfig(c Config) error {
	expected, err := NewConfig(c.groupingPower, c.maxValuePower)
	if err != nil {
		return err
	}
	if c != expected {
		return errors.New("h2histogram: invalid configuration")
	}
	return nil
}

func checkAdd(a, b uint64) error {
	if b > math.MaxUint64-a {
		return errors.New("h2histogram: count overflow")
	}
	return nil
}

func checkedTotal(counts []uint64) (uint64, error) {
	var total uint64
	for _, n := range counts {
		if err := checkAdd(total, n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// CheckedTotalCount returns an error if the sum exceeds uint64.
func (h *Histogram) CheckedTotalCount() (uint64, error) { return checkedTotal(h.buckets) }

// CheckedTotalCount returns an error if the sum exceeds uint64.
func (s *SparseHistogram) CheckedTotalCount() (uint64, error) { return checkedTotal(s.count) }

// Reset clears all counters while retaining their backing storage.
func (h *Histogram) Reset() { clear(h.buckets) }

// CheckedAddAssign adds other after validating geometry and every bucket.
// On error h is unchanged. Self-addition is supported.
func (h *Histogram) CheckedAddAssign(other *Histogram) error {
	if err := h.checkCompatible(other); err != nil {
		return err
	}
	for i, n := range other.buckets {
		if err := checkAdd(h.buckets[i], n); err != nil {
			return err
		}
	}
	for i, n := range other.buckets {
		h.buckets[i] += n
	}
	return nil
}

// CheckedSum produces independent storage from one or more sources. All
// configurations are validated first; repeated source pointers are allowed.
func CheckedSum(histograms []*Histogram) (*Histogram, error) {
	if len(histograms) == 0 {
		return nil, errors.New("h2histogram: cannot sum an empty input")
	}
	first := histograms[0]
	if first == nil {
		return nil, errors.New("h2histogram: nil histogram")
	}
	for _, h := range histograms {
		if err := first.checkCompatible(h); err != nil {
			return nil, err
		}
	}
	out := NewWithConfig(first.config)
	for _, h := range histograms {
		if err := out.CheckedAddAssign(h); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SnapshotInto overwrites an existing compatible destination without allocation.
// This operation and all dense mutations require external synchronization.
func (h *Histogram) SnapshotInto(dst *Histogram) error {
	if err := h.checkCompatible(dst); err != nil {
		return err
	}
	copy(dst.buckets, h.buckets)
	return nil
}

// DrainInto snapshots into dst then resets h. Self-drain and destinations
// sharing the source backing slice (including shallow copies) are rejected.
func (h *Histogram) DrainInto(dst *Histogram) error {
	if h == dst || (dst != nil && len(h.buckets) != 0 && len(dst.buckets) != 0 && &h.buckets[0] == &dst.buckets[0]) {
		return errors.New("h2histogram: cannot drain into self")
	}
	if err := h.SnapshotInto(dst); err != nil {
		return err
	}
	h.Reset()
	return nil
}

func validatePercentile(p float64) error {
	if math.IsNaN(p) || p < 0 || p > 1 {
		return fmt.Errorf("h2histogram: percentile must be in [0, 1], got %v", p)
	}
	return nil
}

func scanBucket(config Config, index []int, count []uint64, target uint64) Bucket {
	var running uint64
	for k, n := range count {
		running += n
		if n != 0 && running >= target {
			i := k
			if index != nil {
				i = index[k]
			}
			start, end := config.IndexToRange(i)
			return Bucket{Count: n, Start: start, End: end}
		}
	}
	return Bucket{}
}

func prepareResults(ps []float64, dst []PercentileResult) ([]PercentileResult, error) {
	for _, p := range ps {
		if err := validatePercentile(p); err != nil {
			return nil, err
		}
	}
	if cap(dst) < len(ps) {
		return make([]PercentileResult, len(ps)), nil
	}
	return dst[:len(ps)], nil
}

func scanPercentiles(config Config, index []int, count []uint64, ps []float64, dst []PercentileResult) ([]PercentileResult, error) {
	// Validate before touching caller output, and compute the total only once.
	for _, p := range ps {
		if err := validatePercentile(p); err != nil {
			return nil, err
		}
	}
	total, err := checkedTotal(count)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, nil
	}
	dst, err = prepareResults(ps, dst)
	if err != nil {
		return nil, err
	}
	for i, p := range ps {
		dst[i] = PercentileResult{Percentile: p, Bucket: scanBucket(config, index, count, ceilCount(p, total))}
	}
	return dst, nil
}

// PercentilesInto reuses dst capacity, replacing its contents and length. Empty
// histograms return nil. Requests retain their order and duplicates. Each query
// scans buckets independently; no sorting scratch or maps are allocated.
func (h *Histogram) PercentilesInto(ps []float64, dst []PercentileResult) ([]PercentileResult, error) {
	return scanPercentiles(h.config, nil, h.buckets, ps, dst)
}

// PercentilesInto reuses dst and scans only sparse storage.
func (s *SparseHistogram) PercentilesInto(ps []float64, dst []PercentileResult) ([]PercentileResult, error) {
	return scanPercentiles(s.config, s.index, s.count, ps, dst)
}

func (c *CumulativeHistogram) percentileBucket(p float64) Bucket {
	pos := c.findQuantilePosition(ceilCount(p, c.TotalCount()))
	start, end := c.config.IndexToRange(c.index[pos])
	return Bucket{Count: c.individualCount(pos), Start: start, End: end}
}

// PercentilesInto reuses dst capacity and binary-searches each requested rank.
func (c *CumulativeHistogram) PercentilesInto(ps []float64, dst []PercentileResult) ([]PercentileResult, error) {
	for _, p := range ps {
		if err := validatePercentile(p); err != nil {
			return nil, err
		}
	}
	if c.IsEmpty() {
		return nil, nil
	}
	out, err := prepareResults(ps, dst)
	if err != nil {
		return nil, err
	}
	for i, p := range ps {
		out[i] = PercentileResult{Percentile: p, Bucket: c.percentileBucket(p)}
	}
	return out, nil
}

// CheckedToCumulative creates an independent snapshot, rejecting total overflow.
func (h *Histogram) CheckedToCumulative() (*CumulativeHistogram, error) {
	if _, err := h.CheckedTotalCount(); err != nil {
		return nil, err
	}
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
	return newCumulative(h.config, index, count), nil
}

// CheckedToCumulative creates an independent snapshot, rejecting total overflow.
func (s *SparseHistogram) CheckedToCumulative() (*CumulativeHistogram, error) {
	if _, err := s.CheckedTotalCount(); err != nil {
		return nil, err
	}
	index := append([]int(nil), s.index...)
	count := make([]uint64, len(s.count))
	var running uint64
	for i, n := range s.count {
		running += n
		count[i] = running
	}
	return newCumulative(s.config, index, count), nil
}

// ToSparse converts prefixes to independent individual counts without dense storage.
func (c *CumulativeHistogram) ToSparse() *SparseHistogram {
	out := &SparseHistogram{config: c.config}
	for i, index := range c.index {
		n := c.individualCount(i)
		if n != 0 {
			out.index = append(out.index, index)
			out.count = append(out.count, n)
		}
	}
	return out
}

// Merge merges sorted sparse entries, rejecting configuration or bucket overflow.
func (s *SparseHistogram) Merge(other *SparseHistogram) (*SparseHistogram, error) {
	if other == nil || s.config != other.config {
		return nil, errors.New("h2histogram: histograms have incompatible configurations")
	}
	out := &SparseHistogram{config: s.config}
	i, j := 0, 0
	for i < len(s.index) || j < len(other.index) {
		var index int
		var n uint64
		if j == len(other.index) || (i < len(s.index) && s.index[i] < other.index[j]) {
			index, n = s.index[i], s.count[i]
			i++
		} else if i == len(s.index) || other.index[j] < s.index[i] {
			index, n = other.index[j], other.count[j]
			j++
		} else {
			index = s.index[i]
			if err := checkAdd(s.count[i], other.count[j]); err != nil {
				return nil, err
			}
			n = s.count[i] + other.count[j]
			i++
			j++
		}
		out.index = append(out.index, index)
		out.count = append(out.count, n)
	}
	return out, nil
}

// Downsample coalesces sorted entries directly into coarser geometry.
func (s *SparseHistogram) Downsample(groupingPower uint32) (*SparseHistogram, error) {
	if groupingPower >= s.config.groupingPower {
		return nil, errors.New("h2histogram: target grouping_power must be less than the current grouping_power")
	}
	config, err := NewConfig(groupingPower, s.config.maxValuePower)
	if err != nil {
		return nil, err
	}
	out := &SparseHistogram{config: config}
	for k, index := range s.index {
		target, err := config.ValueToIndex(s.config.IndexToLowerBound(index))
		if err != nil {
			return nil, err
		}
		last := len(out.index) - 1
		if last >= 0 && out.index[last] == target {
			if err := checkAdd(out.count[last], s.count[k]); err != nil {
				return nil, err
			}
			out.count[last] += s.count[k]
		} else {
			out.index = append(out.index, target)
			out.count = append(out.count, s.count[k])
		}
	}
	return out, nil
}

// Merge combines snapshots without dense reconstruction, checking total overflow.
func (c *CumulativeHistogram) Merge(other *CumulativeHistogram) (*CumulativeHistogram, error) {
	if other == nil || c.config != other.config {
		return nil, errors.New("h2histogram: histograms have incompatible configurations")
	}
	s, err := c.ToSparse().Merge(other.ToSparse())
	if err != nil {
		return nil, err
	}
	return s.CheckedToCumulative()
}

// Downsample coalesces individual counts and recomputes the midpoint mean.
func (c *CumulativeHistogram) Downsample(groupingPower uint32) (*CumulativeHistogram, error) {
	s, err := c.ToSparse().Downsample(groupingPower)
	if err != nil {
		return nil, err
	}
	return s.CheckedToCumulative()
}

// Compact releases spare slice capacity. It may allocate and requires exclusive access.
func (s *SparseHistogram) Compact() {
	if cap(s.index) != len(s.index) {
		v := make([]int, len(s.index))
		copy(v, s.index)
		s.index = v
	}
	if cap(s.count) != len(s.count) {
		v := make([]uint64, len(s.count))
		copy(v, s.count)
		s.count = v
	}
}

// Compact releases spare slice capacity. It may allocate and requires exclusive access.
func (c *CumulativeHistogram) Compact() {
	if cap(c.index) != len(c.index) {
		v := make([]int, len(c.index))
		copy(v, c.index)
		c.index = v
	}
	if cap(c.count) != len(c.count) {
		v := make([]uint64, len(c.count))
		copy(v, c.count)
		c.count = v
	}
}
