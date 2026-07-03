package h2histogram

import "fmt"

// Bucket is a single histogram bucket: a count and an inclusive value range.
type Bucket struct {
	// Count is the number of observations in the bucket.
	Count uint64
	// Start is the inclusive lower bound of the bucket's value range.
	Start uint64
	// End is the inclusive upper bound of the bucket's value range.
	End uint64
}

// Range returns the inclusive (start, end) range of the bucket.
func (b Bucket) Range() (uint64, uint64) {
	return b.Start, b.End
}

// Midpoint returns the arithmetic midpoint of the bucket range. It is a
// reasonable point estimate for values that fell into this bucket.
func (b Bucket) Midpoint() float64 {
	return (float64(b.Start) + float64(b.End)) / 2.0
}

// Width returns the number of distinct integer values the bucket covers.
func (b Bucket) Width() uint64 {
	return b.End - b.Start + 1
}

// String implements fmt.Stringer.
func (b Bucket) String() string {
	return fmt.Sprintf("Bucket(count=%d, range=[%d, %d])", b.Count, b.Start, b.End)
}
