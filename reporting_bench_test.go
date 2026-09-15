package h2histogram

import "testing"

var benchmarkBucket *Bucket
var benchmarkDense *Histogram
var benchmarkSparse *SparseHistogram
var benchmarkCumulative *CumulativeHistogram
var benchmarkResults []PercentileResult

// Inputs and reusable destinations are prepared outside each timed phase.
// Owned phases include construction and normal GC allocation pressure.
func BenchmarkReportingPhases(b *testing.B) {
	h, _ := New(7, 32)
	for v := uint64(1); v < 1<<32; v = v*3/2 + 1 {
		_ = h.Record(v, 3)
	}
	sparse := h.ToSparse()
	cumulative := h.ToCumulative()
	ps := []float64{1, .5, .99, 0, .5}
	dst, _ := New(7, 32)
	drainDst, _ := New(7, 32)
	results := make([]PercentileResult, len(ps))
	b.Run("record", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = dst.Increment(1000)
		}
	})
	b.Run("reset", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dst.Reset()
		}
	})
	b.Run("snapshot_reused", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = h.SnapshotInto(dst)
		}
	})
	b.Run("drain_reused", func(b *testing.B) {
		// Snapshot refill is timed too so every drained source contains data.
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = h.SnapshotInto(dst)
			_ = dst.DrainInto(drainDst)
		}
	})
	b.Run("checked_add_reused", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = dst.CheckedAddAssign(h)
		}
	})
	b.Run("checked_sum_owned", func(b *testing.B) {
		sources := []*Histogram{h, h, h, h}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkDense, _ = CheckedSum(sources)
		}
	})
	for _, tc := range []struct {
		name string
		q    interface {
			Percentile(float64) (*Bucket, error)
			Percentiles([]float64) ([]PercentileResult, error)
			PercentilesInto([]float64, []PercentileResult) ([]PercentileResult, error)
		}
	}{{"dense", h}, {"sparse", sparse}, {"cumulative", cumulative}} {
		b.Run(tc.name+"_scalar", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkBucket, _ = tc.q.Percentile(.99)
			}
		})
		b.Run(tc.name+"_batch_owned", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkResults, _ = tc.q.Percentiles(ps)
			}
		})
		b.Run(tc.name+"_batch_reused", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkResults, _ = tc.q.PercentilesInto(ps, results)
			}
		})
	}
	b.Run("sparse_snapshot_owned", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkSparse = h.ToSparse()
		}
	})
	b.Run("cumulative_snapshot_owned", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkCumulative, _ = h.CheckedToCumulative()
		}
	})
	b.Run("sparse_merge_owned", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkSparse, _ = sparse.Merge(sparse)
		}
	})
	b.Run("cumulative_downsample_owned", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkCumulative, _ = cumulative.Downsample(5)
		}
	})
}
