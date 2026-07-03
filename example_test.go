package h2histogram_test

import (
	"fmt"

	h2histogram "github.com/iopsystems/h2histogram-go"
)

// Example demonstrates recording values and querying a percentile.
func Example() {
	h, err := h2histogram.New(7, 64) // groupingPower=7, maxValuePower=64
	if err != nil {
		panic(err)
	}

	for i := uint64(1); i <= 100; i++ {
		if err := h.Increment(i); err != nil {
			panic(err)
		}
	}

	fmt.Println("total:", h.TotalCount())

	p50, _ := h.Percentile(0.5)
	fmt.Printf("p50 range: [%d, %d]\n", p50.Start, p50.End)

	// Output:
	// total: 100
	// p50 range: [50, 50]
}

// ExampleCumulativeHistogram shows the fast read-only query path.
func ExampleCumulativeHistogram() {
	h, _ := h2histogram.New(7, 64)
	_ = h.Record(10, 1)
	_ = h.Record(20, 1)
	_ = h.Record(30, 1)

	c := h.ToCumulative()
	mean, _ := c.Mean()
	fmt.Printf("mean: %.1f\n", mean)

	// Output:
	// mean: 20.0
}
