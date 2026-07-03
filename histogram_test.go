package h2histogram

import "testing"

func mustHist(t *testing.T, gp, mvp uint32) *Histogram {
	t.Helper()
	h, err := New(gp, mvp)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", gp, mvp, err)
	}
	return h
}

func TestIncrementAndTotal(t *testing.T) {
	h := mustHist(t, 7, 64)
	for i := uint64(0); i <= 100; i++ {
		if err := h.Increment(i); err != nil {
			t.Fatal(err)
		}
	}
	if h.TotalCount() != 101 {
		t.Errorf("TotalCount = %d, want 101", h.TotalCount())
	}
}

func TestRecordWithCount(t *testing.T) {
	h := mustHist(t, 7, 64)
	if err := h.Record(100, 5); err != nil {
		t.Fatal(err)
	}
	if h.TotalCount() != 5 {
		t.Errorf("TotalCount = %d, want 5", h.TotalCount())
	}
	idx, _ := h.Config().ValueToIndex(100)
	if h.Buckets()[idx] != 5 {
		t.Errorf("bucket[%d] = %d, want 5", idx, h.Buckets()[idx])
	}
}

func TestPercentileExactLowRange(t *testing.T) {
	// In the linear region (values < cutoff) buckets have width 1, so
	// percentiles are exact.
	h := mustHist(t, 7, 64)
	for i := uint64(1); i <= 100; i++ {
		if err := h.Increment(i); err != nil {
			t.Fatal(err)
		}
	}
	check := func(q float64, wantStart, wantEnd uint64) {
		b, err := h.Percentile(q)
		if err != nil {
			t.Fatal(err)
		}
		if b == nil || b.Start != wantStart || b.End != wantEnd {
			t.Errorf("Percentile(%v) = %v, want [%d, %d]", q, b, wantStart, wantEnd)
		}
	}
	check(0.5, 50, 50)
	check(1.0, 100, 100)
	check(0.0, 1, 1)
}

func TestPercentileEmpty(t *testing.T) {
	h := mustHist(t, 7, 64)
	b, err := h.Percentile(0.5)
	if err != nil || b != nil {
		t.Errorf("empty Percentile = %v, %v; want nil, nil", b, err)
	}
	res, err := h.Percentiles([]float64{0.5, 0.9})
	if err != nil || res != nil {
		t.Errorf("empty Percentiles = %v, %v; want nil, nil", res, err)
	}
}

func TestPercentilesOrderPreserved(t *testing.T) {
	h := mustHist(t, 7, 64)
	for i := uint64(0); i < 1000; i++ {
		_ = h.Increment(i)
	}
	res, err := h.Percentiles([]float64{0.9, 0.5, 0.99})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0.9, 0.5, 0.99}
	for i, r := range res {
		if r.Percentile != want[i] {
			t.Errorf("order not preserved: got %v, want %v", res, want)
		}
	}
}

func TestPercentileInvalid(t *testing.T) {
	h := mustHist(t, 7, 64)
	_ = h.Increment(1)
	if _, err := h.Percentile(1.5); err == nil {
		t.Error("expected error for percentile 1.5")
	}
}

func TestMerge(t *testing.T) {
	a := mustHist(t, 7, 64)
	b := mustHist(t, 7, 64)
	_ = a.Record(10, 3)
	_ = b.Record(10, 4)
	_ = b.Record(2000, 1)
	merged, err := a.Merge(b)
	if err != nil {
		t.Fatal(err)
	}
	if merged.TotalCount() != 8 {
		t.Errorf("TotalCount = %d, want 8", merged.TotalCount())
	}
	idx, _ := merged.Config().ValueToIndex(10)
	if merged.Buckets()[idx] != 7 {
		t.Errorf("bucket[%d] = %d, want 7", idx, merged.Buckets()[idx])
	}
}

func TestMergeIncompatible(t *testing.T) {
	a := mustHist(t, 7, 64)
	b := mustHist(t, 6, 64)
	if _, err := a.Merge(b); err == nil {
		t.Error("expected error merging incompatible configs")
	}
}

func TestSubtract(t *testing.T) {
	a := mustHist(t, 7, 64)
	b := mustHist(t, 7, 64)
	_ = a.Record(10, 5)
	_ = b.Record(10, 2)
	diff, err := a.Subtract(b)
	if err != nil {
		t.Fatal(err)
	}
	if diff.TotalCount() != 3 {
		t.Errorf("TotalCount = %d, want 3", diff.TotalCount())
	}
	if _, err := b.Subtract(a); err == nil {
		t.Error("expected error for negative subtraction")
	}
}

func TestFromBucketsRoundtrip(t *testing.T) {
	h := mustHist(t, 3, 64)
	_ = h.Record(5, 2)
	_ = h.Record(1000, 7)
	h2, err := FromBuckets(3, 64, h.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Equal(h2) {
		t.Error("FromBuckets roundtrip mismatch")
	}
}

func TestFromBucketsWrongLength(t *testing.T) {
	if _, err := FromBuckets(7, 64, []uint64{0, 0, 0}); err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestDownsample(t *testing.T) {
	h := mustHist(t, 7, 64)
	for i := uint64(0); i < 10000; i++ {
		_ = h.Increment(i)
	}
	coarse, err := h.Downsample(3)
	if err != nil {
		t.Fatal(err)
	}
	if coarse.Config().GroupingPower() != 3 {
		t.Errorf("grouping power = %d, want 3", coarse.Config().GroupingPower())
	}
	if coarse.TotalCount() != h.TotalCount() {
		t.Errorf("total count changed: %d != %d", coarse.TotalCount(), h.TotalCount())
	}
	if _, err := h.Downsample(7); err == nil {
		t.Error("expected error downsampling to same grouping power")
	}
}

func TestSparseRoundtrip(t *testing.T) {
	h := mustHist(t, 7, 64)
	_ = h.Record(1, 1)
	_ = h.Record(500, 3)
	_ = h.Record(999999, 2)
	sparse := h.ToSparse()
	if sparse.TotalCount() != h.TotalCount() {
		t.Errorf("sparse total %d != %d", sparse.TotalCount(), h.TotalCount())
	}
	if sparse.Len() != 3 {
		t.Errorf("sparse len = %d, want 3", sparse.Len())
	}
	prev := -1
	for _, i := range sparse.Index() {
		if i <= prev {
			t.Error("sparse indices not strictly ascending")
		}
		prev = i
	}
	if !sparse.ToDense().Equal(h) {
		t.Error("sparse->dense roundtrip mismatch")
	}
}

func TestSparseFromPartsValidation(t *testing.T) {
	c := mustConfig(t, 7, 64)
	if _, err := SparseFromParts(c, []int{1, 2}, []uint64{1}); err == nil {
		t.Error("expected length-mismatch error")
	}
	if _, err := SparseFromParts(c, []int{2, 1}, []uint64{1, 1}); err == nil {
		t.Error("expected not-ascending error")
	}
	if _, err := SparseFromParts(c, []int{999999999}, []uint64{1}); err == nil {
		t.Error("expected out-of-range error")
	}
}

func TestRecordManyMatchesLoop(t *testing.T) {
	base := []uint64{0, 1, 2, 300, 255, 256, 1024, 1_000_000, (1 << 50) + 3}
	var values []uint64
	for i := 0; i < 111; i++ {
		values = append(values, base...)
	}
	a := mustHist(t, 7, 64)
	for _, v := range values {
		_ = a.Increment(v)
	}
	b := mustHist(t, 7, 64)
	if err := b.RecordMany(values); err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Error("RecordMany != loop")
	}
}

func TestRecordManyWithCounts(t *testing.T) {
	a := mustHist(t, 7, 64)
	if err := a.RecordManyWithCounts([]uint64{10, 20, 10}, []uint64{2, 3, 5}); err != nil {
		t.Fatal(err)
	}
	if a.TotalCount() != 10 {
		t.Errorf("total = %d, want 10", a.TotalCount())
	}
	idx, _ := a.Config().ValueToIndex(10)
	if a.Buckets()[idx] != 7 {
		t.Errorf("bucket[%d] = %d, want 7", idx, a.Buckets()[idx])
	}
}

func TestIterBuckets(t *testing.T) {
	h := mustHist(t, 3, 6)
	_ = h.Increment(0)
	var count int
	h.EachBucket(func(Bucket) { count++ })
	if count != h.Config().TotalBuckets() {
		t.Errorf("iterated %d buckets, want %d", count, h.Config().TotalBuckets())
	}
	nonzero := h.NonzeroBuckets()
	if len(nonzero) != 1 || nonzero[0].Count != 1 {
		t.Errorf("nonzero = %v, want one bucket with count 1", nonzero)
	}
}
