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
