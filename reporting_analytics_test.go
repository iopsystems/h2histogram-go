package h2histogram

import (
	"math"
	"reflect"
	"testing"
)

func TestReportValidationAndSnapshotOwnership(t *testing.T) {
	h, _ := New(3, 16)
	_ = h.Record(500, 7)
	for _, q := range []interface {
		Percentile(float64) (*Bucket, error)
	}{h, h.ToSparse(), h.ToCumulative()} {
		if _, err := q.Percentile(math.NaN()); err == nil {
			t.Error("NaN accepted")
		}
	}
	c := h.ToCumulative()
	c.Count()[0] = 100
	c.Index()[0] = 0
	if !c.ToDense().Equal(h) {
		t.Error("snapshot getters mutated cumulative state")
	}
	s := h.ToSparse()
	s.Count()[0] = 100
	s.Index()[0] = 0
	if !s.ToDense().Equal(h) {
		t.Error("snapshot getters mutated sparse state")
	}
	if _, err := SparseFromParts(Config{}, nil, nil); err == nil {
		t.Error("zero config accepted")
	}
	if _, err := NewConfig(32, 64); err == nil {
		t.Error("overflowing geometry accepted")
	}
}

func TestLifecycleAndCheckedAggregation(t *testing.T) {
	h, _ := New(3, 16)
	_ = h.Record(500, 7)
	before, _ := FromBuckets(3, 16, h.Buckets())
	if _, err := CheckedSum(nil); err == nil {
		t.Fatal("empty sum accepted")
	}
	sum, err := CheckedSum([]*Histogram{h, h})
	if err != nil || sum.TotalCount() != 14 || !h.Equal(before) {
		t.Fatal("owned repeated sum", err)
	}
	single, _ := CheckedSum([]*Histogram{h})
	single.Reset()
	if !h.Equal(before) {
		t.Fatal("singleton aliases")
	}
	dst, _ := New(3, 16)
	backing := &dst.Buckets()[0]
	if err := h.SnapshotInto(dst); err != nil || !dst.Equal(h) || backing != &dst.Buckets()[0] {
		t.Fatal("snapshot", err)
	}
	if err := h.DrainInto(h); err == nil {
		t.Fatal("self drain accepted")
	}
	if err := h.DrainInto(dst); err != nil || h.TotalCount() != 0 || !dst.Equal(before) {
		t.Fatal("drain", err)
	}
	bad, _ := New(2, 16)
	_ = h.Record(2, 1)
	if err := h.DrainInto(bad); err == nil || h.TotalCount() != 1 {
		t.Fatal("incompatible drain mutated")
	}
	a, _ := New(3, 16)
	b, _ := New(3, 16)
	a.buckets[1] = math.MaxUint64
	b.buckets[0] = 1
	b.buckets[1] = 1
	old := append([]uint64(nil), a.buckets...)
	if err := a.CheckedAddAssign(b); err == nil || !reflect.DeepEqual(a.buckets, old) {
		t.Fatal("overflow mutation")
	}
	if _, err := CheckedSum([]*Histogram{a, b}); err == nil {
		t.Fatal("sum overflow")
	}
	if _, err := CheckedSum([]*Histogram{a, b, bad}); err == nil || err.Error() != "h2histogram: histograms have incompatible configurations" {
		t.Fatal("configs must be checked first", err)
	}
	b.Reset()
	b.buckets[2] = 3
	if err := b.CheckedAddAssign(b); err != nil || b.buckets[2] != 6 {
		t.Fatal("self add", err)
	}
}

func TestNativeReportsAndTransforms(t *testing.T) {
	h, _ := New(3, 16)
	for _, v := range []uint64{0, 9, 9, 500, 500, 65000} {
		_ = h.Increment(v)
	}
	s := h.ToSparse()
	c := h.ToCumulative()
	ps := []float64{1, 0, .5, .5, .9}
	want, _ := h.Percentiles(ps)
	for _, q := range []interface {
		PercentilesInto([]float64, []PercentileResult) ([]PercentileResult, error)
		Percentile(float64) (*Bucket, error)
	}{h, s, c} {
		out := make([]PercentileResult, len(ps))
		ptr := &out[0]
		got, err := q.PercentilesInto(ps, out)
		if err != nil || !reflect.DeepEqual(got, want) || &got[0] != ptr {
			t.Fatal("batch", got, err)
		}
		for i, p := range ps {
			b, err := q.Percentile(p)
			if err != nil || *b != want[i].Bucket {
				t.Fatal("scalar", p, b, err)
			}
		}
		if got, err := q.PercentilesInto(nil, out); err != nil || len(got) != 0 {
			t.Fatal("empty requests", err)
		}
	}
	if !c.ToSparse().Equal(s) {
		t.Fatal("cumulative sparse")
	}
	sd, err := s.Downsample(1)
	if err != nil {
		t.Fatal(err)
	}
	dense, _ := h.Downsample(1)
	if !sd.ToDense().Equal(dense) {
		t.Fatal("sparse downsample")
	}
	cd, err := c.Downsample(1)
	if err != nil || !cd.ToDense().Equal(dense) {
		t.Fatal("cumulative downsample", err)
	}
	wm, _ := dense.ToCumulative().Mean()
	gm, _ := cd.Mean()
	if gm != wm {
		t.Fatal("downsample mean", gm, wm)
	}
	sm, err := s.Merge(s)
	if err != nil || sm.TotalCount() != 12 {
		t.Fatal("sparse merge", err)
	}
	cm, err := c.Merge(c)
	if err != nil || !cm.ToSparse().Equal(sm) {
		t.Fatal("cumulative merge", err)
	}
	s.Compact()
	c.Compact()
	if cap(s.index) != len(s.index) || cap(c.count) != len(c.count) {
		t.Fatal("compaction")
	}
}

func TestCountBounds(t *testing.T) {
	h, _ := New(3, 16)
	h.buckets[0] = math.MaxUint64 - 1
	h.buckets[1] = 1
	for _, q := range []interface {
		Percentile(float64) (*Bucket, error)
	}{h, h.ToSparse(), h.ToCumulative()} {
		b, e := q.Percentile(1)
		if e != nil || b.Start != 1 {
			t.Fatal("max count endpoint", b, e)
		}
	}
	h.buckets[1] = 2
	if _, e := h.CheckedTotalCount(); e == nil {
		t.Fatal("total overflow")
	}
	if _, e := h.CheckedToCumulative(); e == nil {
		t.Fatal("dense cumulative overflow")
	}
	if _, e := h.ToSparse().CheckedToCumulative(); e == nil {
		t.Fatal("sparse cumulative overflow")
	}
	if _, e := h.Percentile(.5); e == nil {
		t.Fatal("query total overflow")
	}
	a, _ := New(3, 16)
	a.buckets[10] = math.MaxUint64
	a.buckets[11] = 1
	if _, e := a.ToSparse().Downsample(0); e == nil {
		t.Fatal("downsample overflow")
	}
	defer func() {
		if recover() == nil {
			t.Error("legacy conversion must panic on overflow")
		}
	}()
	h.ToCumulative()
}

func TestOverflowAtEveryBucketPosition(t *testing.T) {
	a, _ := New(2, 8)
	b, _ := New(2, 8)
	for _, pos := range []int{0, a.Len() / 2, a.Len() - 1} {
		a.Reset()
		b.Reset()
		a.buckets[pos] = math.MaxUint64
		b.buckets[pos] = 1
		original := append([]uint64(nil), a.buckets...)
		if e := a.CheckedAddAssign(b); e == nil || !reflect.DeepEqual(a.buckets, original) {
			t.Fatal("in-place overflow", pos, e)
		}
		if _, e := a.Merge(b); e == nil {
			t.Fatal("dense merge overflow", pos)
		}
		if _, e := a.ToSparse().Merge(b.ToSparse()); e == nil {
			t.Fatal("sparse merge overflow", pos)
		}
		if _, e := a.ToCumulative().Merge(b.ToCumulative()); e == nil {
			t.Fatal("cumulative merge overflow", pos)
		}
	}
	// Each bucket fits, but the combined prefix total does not.
	a.Reset()
	b.Reset()
	a.buckets[0] = math.MaxUint64
	b.buckets[1] = 1
	if _, e := a.ToCumulative().Merge(b.ToCumulative()); e == nil {
		t.Fatal("merged total overflow")
	}
}

func TestImportValidationAndZeroNormalization(t *testing.T) {
	cfg := mustConfig(t, 3, 16)
	for _, tc := range []struct {
		index []int
		count []uint64
	}{{[]int{1}, nil}, {[]int{-1}, []uint64{1}}, {[]int{cfg.TotalBuckets()}, []uint64{1}}, {[]int{2, 1}, []uint64{1, 1}}, {[]int{1, 1}, []uint64{1, 1}}} {
		if _, e := SparseFromParts(cfg, tc.index, tc.count); e == nil {
			t.Fatal("bad sparse accepted", tc)
		}
		if _, e := CumulativeFromParts(cfg, tc.index, tc.count); e == nil {
			t.Fatal("bad cumulative accepted", tc)
		}
	}
	s, e := SparseFromParts(cfg, []int{1, 2, 3}, []uint64{0, 4, 0})
	if e != nil || s.Len() != 1 || s.Index()[0] != 2 {
		t.Fatal("zero normalization", s, e)
	}
	c, e := CumulativeFromParts(cfg, []int{1, 2, 3}, []uint64{4, 4, 8})
	if e != nil || c.ToSparse().Len() != 2 {
		t.Fatal("zero deltas", e)
	}
	if _, e := CumulativeFromParts(Config{}, nil, nil); e == nil {
		t.Fatal("invalid config")
	}
	defer func() {
		if recover() == nil {
			t.Error("NewWithConfig must reject zero configuration")
		}
	}()
	NewWithConfig(Config{})
}

func TestBatchFailurePreservesOutputAndEmptyReports(t *testing.T) {
	h, _ := New(3, 16)
	for _, q := range []interface {
		PercentilesInto([]float64, []PercentileResult) ([]PercentileResult, error)
		Percentile(float64) (*Bucket, error)
	}{h, h.ToSparse(), h.ToCumulative()} {
		if b, e := q.Percentile(.5); b != nil || e != nil {
			t.Fatal("empty scalar", b, e)
		}
		if out, e := q.PercentilesInto([]float64{.5}, nil); out != nil || e != nil {
			t.Fatal("empty batch", out, e)
		}
	}
	h.Increment(10)
	for _, q := range []interface {
		PercentilesInto([]float64, []PercentileResult) ([]PercentileResult, error)
	}{h, h.ToSparse(), h.ToCumulative()} {
		dst := []PercentileResult{{Percentile: 42}}
		for _, p := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -.1, 1.1} {
			if _, e := q.PercentilesInto([]float64{.5, p}, dst); e == nil || dst[0].Percentile != 42 {
				t.Fatal("invalid request mutated output", p, e)
			}
		}
	}
}

func TestNativeTransformsMatchDenseAcrossGeometries(t *testing.T) {
	for gp := uint32(1); gp <= 6; gp++ {
		h, _ := New(gp, 16)
		for v := uint64(0); v < 65536; v += 137 {
			h.Record(v, v%7+1)
		}
		for target := uint32(0); target < gp; target++ {
			want, _ := h.Downsample(target)
			s, e := h.ToSparse().Downsample(target)
			if e != nil || !s.ToDense().Equal(want) {
				t.Fatal("sparse geometry", gp, target, e)
			}
			c, e := h.ToCumulative().Downsample(target)
			if e != nil || !c.ToDense().Equal(want) {
				t.Fatal("cumulative geometry", gp, target, e)
			}
		}
	}
}

func TestReusableReportAllocations(t *testing.T) {
	h, _ := New(3, 16)
	h.Increment(50)
	s := h.ToSparse()
	c := h.ToCumulative()
	ps := []float64{1, .5, 0, .5}
	dst := make([]PercentileResult, len(ps))
	for _, q := range []interface {
		PercentilesInto([]float64, []PercentileResult) ([]PercentileResult, error)
	}{h, s, c} {
		if n := testing.AllocsPerRun(100, func() {
			out, e := q.PercentilesInto(ps, dst)
			if e != nil || len(out) != len(ps) {
				panic("report failed")
			}
		}); n != 0 {
			t.Fatal("reused report allocated", n)
		}
	}
	snapshot, _ := New(3, 16)
	if n := testing.AllocsPerRun(100, func() { h.SnapshotInto(snapshot) }); n != 0 {
		t.Fatal("snapshot allocated", n)
	}
}

func TestDrainRejectsShallowCopyAlias(t *testing.T) {
	h, _ := New(3, 16)
	h.Increment(40)
	alias := *h
	if err := h.DrainInto(&alias); err == nil || h.TotalCount() != 1 {
		t.Fatal("drain accepted shared backing storage", err)
	}
}
