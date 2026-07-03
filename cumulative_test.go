package h2histogram

import (
	"math"
	"testing"
)

func TestCumulativeFromHistogram(t *testing.T) {
	h := mustHist(t, 7, 64)
	_ = h.Record(1, 2)
	_ = h.Record(500, 3)
	_ = h.Record(1_000_000, 5)

	c := h.ToCumulative()
	if c.TotalCount() != 10 {
		t.Errorf("total = %d, want 10", c.TotalCount())
	}
	if c.Len() != 3 {
		t.Errorf("len = %d, want 3", c.Len())
	}
	// Cumulative counts are prefix sums; last equals total.
	got := c.Count()
	want := []uint64{2, 5, 10}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cumulative count[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestCumulativePercentileMatchesDense(t *testing.T) {
	h := mustHist(t, 7, 64)
	for i := uint64(1); i <= 1000; i++ {
		_ = h.Increment(i)
	}
	c := h.ToCumulative()
	for _, q := range []float64{0.0, 0.1, 0.5, 0.9, 0.99, 1.0} {
		dense, err := h.Percentile(q)
		if err != nil {
			t.Fatal(err)
		}
		cum, err := c.Percentile(q)
		if err != nil {
			t.Fatal(err)
		}
		if dense == nil || cum == nil {
			t.Fatalf("nil bucket at q=%v", q)
		}
		if dense.Start != cum.Start || dense.End != cum.End {
			t.Errorf("q=%v: dense [%d,%d] != cumulative [%d,%d]",
				q, dense.Start, dense.End, cum.Start, cum.End)
		}
	}
}

func TestCumulativeEmpty(t *testing.T) {
	h := mustHist(t, 7, 64)
	c := h.ToCumulative()
	if !c.IsEmpty() {
		t.Error("expected empty")
	}
	b, err := c.Percentile(0.5)
	if err != nil || b != nil {
		t.Errorf("empty Percentile = %v, %v; want nil, nil", b, err)
	}
	if _, ok := c.Mean(); ok {
		t.Error("empty Mean should report false")
	}
}

func TestCumulativeMean(t *testing.T) {
	h := mustHist(t, 7, 64)
	// All in the linear (exact) region so the midpoint mean is exact.
	_ = h.Record(10, 1)
	_ = h.Record(20, 1)
	_ = h.Record(30, 1)
	c := h.ToCumulative()
	m, ok := c.Mean()
	if !ok || math.Abs(m-20.0) > 1e-9 {
		t.Errorf("Mean = %v (ok=%v), want 20", m, ok)
	}
}

func TestCumulativeFromParts(t *testing.T) {
	cfg := mustConfig(t, 7, 64)
	c, err := CumulativeFromParts(cfg, []int{1, 256}, []uint64{3, 8})
	if err != nil {
		t.Fatal(err)
	}
	if c.TotalCount() != 8 {
		t.Errorf("total = %d, want 8", c.TotalCount())
	}
	// invalid: zero count
	if _, err := CumulativeFromParts(cfg, []int{1}, []uint64{0}); err == nil {
		t.Error("expected error for zero cumulative count")
	}
	// invalid: decreasing
	if _, err := CumulativeFromParts(cfg, []int{1, 2}, []uint64{5, 3}); err == nil {
		t.Error("expected error for decreasing counts")
	}
}

func TestCumulativeBucketQuantileRange(t *testing.T) {
	h := mustHist(t, 7, 64)
	_ = h.Record(10, 2)
	_ = h.Record(20, 2)
	c := h.ToCumulative()
	lo, hi, ok := c.BucketQuantileRange(0)
	if !ok || lo != 0.0 || math.Abs(hi-0.5) > 1e-9 {
		t.Errorf("range(0) = (%v, %v, %v), want (0, 0.5, true)", lo, hi, ok)
	}
	lo, hi, ok = c.BucketQuantileRange(1)
	if !ok || math.Abs(lo-0.5) > 1e-9 || math.Abs(hi-1.0) > 1e-9 {
		t.Errorf("range(1) = (%v, %v, %v), want (0.5, 1.0, true)", lo, hi, ok)
	}
	if _, _, ok := c.BucketQuantileRange(99); ok {
		t.Error("out-of-range index should report false")
	}
}

func TestSparseToCumulative(t *testing.T) {
	h := mustHist(t, 7, 64)
	_ = h.Record(1, 1)
	_ = h.Record(500, 3)
	sparse := h.ToSparse()
	c := sparse.ToCumulative()
	if c.TotalCount() != 4 {
		t.Errorf("total = %d, want 4", c.TotalCount())
	}
	if !c.ToDense().Equal(h) {
		t.Error("cumulative->dense roundtrip mismatch")
	}
}
