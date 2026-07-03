package h2histogram

import (
	"math"
	"testing"
)

// Expected values here are copied verbatim from src/config.rs in
// https://github.com/iopsystems/histogram so we are guaranteed bit-identical
// bucketing.

func mustConfig(t *testing.T, gp, mvp uint32) Config {
	t.Helper()
	c, err := NewConfig(gp, mvp)
	if err != nil {
		t.Fatalf("NewConfig(%d, %d) failed: %v", gp, mvp, err)
	}
	return c
}

func TestTotalBuckets(t *testing.T) {
	cases := []struct {
		gp, mvp uint32
		want    int
	}{
		{2, 64, 252},
		{7, 64, 7424},
		{14, 64, 835_584},
		{2, 4, 12},
	}
	for _, tc := range cases {
		if got := mustConfig(t, tc.gp, tc.mvp).TotalBuckets(); got != tc.want {
			t.Errorf("TotalBuckets(%d, %d) = %d, want %d", tc.gp, tc.mvp, got, tc.want)
		}
	}
}

func TestValueToIndex(t *testing.T) {
	c := mustConfig(t, 7, 64)
	cases := []struct {
		value uint64
		want  int
	}{
		{0, 0},
		{1, 1},
		{256, 256},
		{257, 256},
		{258, 257},
		{512, 384},
		{515, 384},
		{516, 385},
		{1024, 512},
		{1031, 512},
		{1032, 513},
		{math.MaxUint64 - 1, 7423},
		{math.MaxUint64, 7423},
	}
	for _, tc := range cases {
		got, err := c.ValueToIndex(tc.value)
		if err != nil {
			t.Fatalf("ValueToIndex(%d) error: %v", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("ValueToIndex(%d) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestIndexToLowerBound(t *testing.T) {
	c := mustConfig(t, 7, 64)
	cases := []struct {
		index int
		want  uint64
	}{
		{0, 0},
		{1, 1},
		{256, 256},
		{384, 512},
		{512, 1024},
		{7423, 18_374_686_479_671_623_680},
	}
	for _, tc := range cases {
		if got := c.IndexToLowerBound(tc.index); got != tc.want {
			t.Errorf("IndexToLowerBound(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}
}

func TestIndexToUpperBound(t *testing.T) {
	c := mustConfig(t, 7, 64)
	cases := []struct {
		index int
		want  uint64
	}{
		{0, 0},
		{1, 1},
		{256, 257},
		{384, 515},
		{512, 1031},
		{7423, math.MaxUint64},
	}
	for _, tc := range cases {
		if got := c.IndexToUpperBound(tc.index); got != tc.want {
			t.Errorf("IndexToUpperBound(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}
}

func TestIndexToRange(t *testing.T) {
	c := mustConfig(t, 7, 64)
	cases := []struct {
		index  int
		lo, hi uint64
	}{
		{0, 0, 0},
		{256, 256, 257},
		{384, 512, 515},
		{512, 1024, 1031},
		{7423, 18_374_686_479_671_623_680, math.MaxUint64},
	}
	for _, tc := range cases {
		lo, hi := c.IndexToRange(tc.index)
		if lo != tc.lo || hi != tc.hi {
			t.Errorf("IndexToRange(%d) = (%d, %d), want (%d, %d)", tc.index, lo, hi, tc.lo, tc.hi)
		}
	}
}

func TestRoundtripValueIndexRange(t *testing.T) {
	c := mustConfig(t, 7, 64)
	values := []uint64{0, 1, 5, 127, 128, 255, 256, 257, 999, 1_000_000, (1 << 40) + 7}
	for _, v := range values {
		idx, err := c.ValueToIndex(v)
		if err != nil {
			t.Fatalf("ValueToIndex(%d): %v", v, err)
		}
		lo, hi := c.IndexToRange(idx)
		if !(lo <= v && v <= hi) {
			t.Errorf("value %d landed in bucket %d [%d, %d]", v, idx, lo, hi)
		}
	}
}

func TestError(t *testing.T) {
	if got := mustConfig(t, 7, 64).Error(); math.Abs(got-100.0/128) > 1e-12 {
		t.Errorf("Error(7,64) = %v, want ~%v", got, 100.0/128)
	}
	// No logarithmic buckets -> zero error.
	if got := mustConfig(t, 3, 4).Error(); got != 0.0 {
		t.Errorf("Error(3,4) = %v, want 0", got)
	}
}

func TestInvalidParams(t *testing.T) {
	cases := [][2]uint32{{7, 65}, {64, 64}, {10, 5}}
	for _, tc := range cases {
		if _, err := NewConfig(tc[0], tc[1]); err == nil {
			t.Errorf("NewConfig(%d, %d) expected error", tc[0], tc[1])
		}
	}
}

func TestConfigFromTotalBuckets(t *testing.T) {
	c, err := ConfigFromTotalBuckets(7424, 64)
	if err != nil {
		t.Fatal(err)
	}
	if c.GroupingPower() != 7 || c.MaxValuePower() != 64 {
		t.Errorf("got gp=%d mvp=%d, want 7/64", c.GroupingPower(), c.MaxValuePower())
	}
	// Rezolus default.
	c2, err := ConfigFromTotalBuckets(496, 64)
	if err != nil {
		t.Fatal(err)
	}
	if c2.GroupingPower() != 3 {
		t.Errorf("got gp=%d, want 3", c2.GroupingPower())
	}
	if _, err := ConfigFromTotalBuckets(7425, 64); err == nil {
		t.Error("expected error for 7425 buckets")
	}
}

func TestOutOfRange(t *testing.T) {
	c := mustConfig(t, 2, 4) // max = 15
	got, err := c.ValueToIndex(15)
	if err != nil {
		t.Fatal(err)
	}
	if got != c.TotalBuckets()-1 {
		t.Errorf("ValueToIndex(15) = %d, want %d", got, c.TotalBuckets()-1)
	}
	if _, err := c.ValueToIndex(16); err == nil {
		t.Error("expected out-of-range error for 16")
	}
}
