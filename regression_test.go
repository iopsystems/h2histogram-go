package h2histogram

import (
	"math"
	"testing"
)

func TestNaNRegression(t *testing.T) {
	h, _ := New(3, 16)
	h.Increment(4)
	if _, e := h.Percentile(math.NaN()); e == nil {
		t.Fatal("NaN accepted")
	}
}
func TestMutableSnapshotRegression(t *testing.T) {
	h, _ := New(3, 16)
	h.Increment(4)
	c := h.ToCumulative()
	c.Count()[0] = 9
	if c.TotalCount() != 1 {
		t.Fatal("getter mutates read-only snapshot")
	}
}

func TestLegacyCombinationRejectsZeroConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		combine func(*Histogram, *Histogram) (*Histogram, error)
	}{
		{"Merge", (*Histogram).Merge},
		{"Subtract", (*Histogram).Subtract},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("zero configuration panicked instead of returning an error: %v", p)
				}
			}()
			got, err := tc.combine(&Histogram{}, &Histogram{})
			if err == nil || got != nil {
				t.Fatalf("zero configuration: got %v, %v; want nil histogram and error", got, err)
			}
		})
	}
}
