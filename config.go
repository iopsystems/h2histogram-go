// Package h2histogram is a Go implementation of the iopsystems h2 histogram.
//
// It produces histograms with byte-for-byte identical bucketing to the Rust
// histogram crate (https://github.com/iopsystems/histogram), so histograms
// recorded here can be consumed by (and interoperate with) Rezolus and the
// Python and JavaScript implementations.
//
// The bucketing strategy is fully determined by two parameters:
//
//   - groupingPower: the number of buckets used to span consecutive powers of
//     two. It controls the relative error, 2^-groupingPower. For example
//     groupingPower=7 gives a relative error of ~0.78%.
//   - maxValuePower: the largest representable value is 2^maxValuePower - 1.
//
// The layout has two regions: a linear region covering
// 0 .. 2^(groupingPower+1) where every bucket has width 1 (exact), and a
// logarithmic region above the cutoff subdivided into 2^groupingPower buckets
// per power of two.
package h2histogram

import (
	"fmt"
	"math/bits"
)

// Config is an immutable bucketing configuration.
//
// Construct one with NewConfig, which validates the parameters and mirrors the
// Rust crate's constructor. It is a faithful port of the Config type from the
// Rust histogram crate.
type Config struct {
	groupingPower     uint32
	maxValuePower     uint32
	max               uint64
	cutoffPower       uint32
	cutoffValue       uint64
	lowerBinCount     uint32
	upperBinDivisions uint32
	upperBinCount     uint32
}

// NewConfig creates and validates a Config.
//
// It returns an error if the parameters are out of range, matching the
// constraints enforced by the Rust crate:
//
//   - maxValuePower must be in the range 0..=64
//   - groupingPower must be less than maxValuePower
func NewConfig(groupingPower, maxValuePower uint32) (Config, error) {
	if maxValuePower > 64 {
		return Config{}, fmt.Errorf("h2histogram: max_value_power must be <= 64, got %d", maxValuePower)
	}
	if groupingPower >= maxValuePower {
		return Config{}, fmt.Errorf(
			"h2histogram: grouping_power (%d) must be less than max_value_power (%d)",
			groupingPower, maxValuePower)
	}

	// The cutoff is the point at which the linear divisions and the
	// logarithmic subdivisions have the same width: cutoffPower = groupingPower + 1.
	cutoffPower := groupingPower + 1
	cutoffValue := uint64(1) << cutoffPower
	upperBinDivisions := uint32(1) << groupingPower

	var max uint64
	if maxValuePower == 64 {
		max = ^uint64(0)
	} else {
		max = (uint64(1) << maxValuePower) - 1
	}

	lowerBinCount := uint32(cutoffValue)
	upperBinCount := (maxValuePower - cutoffPower) * upperBinDivisions

	return Config{
		groupingPower:     groupingPower,
		maxValuePower:     maxValuePower,
		max:               max,
		cutoffPower:       cutoffPower,
		cutoffValue:       cutoffValue,
		lowerBinCount:     lowerBinCount,
		upperBinDivisions: upperBinDivisions,
		upperBinCount:     upperBinCount,
	}, nil
}

// ConfigFromTotalBuckets infers a Config from a known bucket count and
// maxValuePower.
//
// Rezolus/metriken parquet files store dense histograms as a bare list of
// bucket counts without recording groupingPower. Given the number of buckets
// and the (conventionally fixed) maxValuePower the grouping power can be
// recovered uniquely. It returns an error if no grouping power produces
// totalBuckets.
func ConfigFromTotalBuckets(totalBuckets int, maxValuePower uint32) (Config, error) {
	for groupingPower := uint32(0); groupingPower < maxValuePower; groupingPower++ {
		candidate, err := NewConfig(groupingPower, maxValuePower)
		if err != nil {
			continue
		}
		if candidate.TotalBuckets() == totalBuckets {
			return candidate, nil
		}
	}
	return Config{}, fmt.Errorf(
		"h2histogram: no grouping_power with max_value_power=%d yields %d buckets",
		maxValuePower, totalBuckets)
}

// GroupingPower returns the grouping power used to create this configuration.
func (c Config) GroupingPower() uint32 { return c.groupingPower }

// MaxValuePower returns the max value power used to create this configuration.
func (c Config) MaxValuePower() uint32 { return c.maxValuePower }

// Max returns the largest value representable by this configuration,
// i.e. 2^maxValuePower - 1.
func (c Config) Max() uint64 { return c.max }

// TotalBuckets returns the total number of buckets for this configuration.
func (c Config) TotalBuckets() int {
	return int(c.lowerBinCount + c.upperBinCount)
}

// Error returns the relative error (as a percentage) of the logarithmic
// buckets. Linear buckets have width 1 and no error. If the config has no
// logarithmic buckets the error is zero.
func (c Config) Error() float64 {
	if c.groupingPower == c.maxValuePower-1 {
		return 0.0
	}
	return 100.0 / float64(uint64(1)<<c.groupingPower)
}

// ValueToIndex returns the bucket index that value falls into. It returns an
// error if the value is greater than the configured maximum.
func (c Config) ValueToIndex(value uint64) (int, error) {
	if value < c.cutoffValue {
		return int(value), nil
	}
	if value > c.max {
		return 0, fmt.Errorf("h2histogram: value %d is out of range for max %d", value, c.max)
	}

	// power = floor(log2(value)); equivalent to 63 - leading_zeros for u64.
	power := uint32(63 - bits.LeadingZeros64(value))
	logBin := power - c.cutoffPower
	offset := (value - (uint64(1) << power)) >> (power - c.groupingPower)

	return int(c.lowerBinCount + logBin*c.upperBinDivisions + uint32(offset)), nil
}

// IndexToLowerBound returns the inclusive lower bound of the bucket at index.
func (c Config) IndexToLowerBound(index int) uint64 {
	g := uint64(index) >> c.groupingPower
	h := uint64(index) - g*(uint64(1)<<c.groupingPower)
	if g < 1 {
		return h
	}
	return (uint64(1) << (uint64(c.groupingPower) + g - 1)) + (uint64(1)<<(g-1))*h
}

// IndexToUpperBound returns the inclusive upper bound of the bucket at index.
func (c Config) IndexToUpperBound(index int) uint64 {
	if uint32(index) == c.lowerBinCount+c.upperBinCount-1 {
		return c.max
	}
	g := uint64(index) >> c.groupingPower
	h := uint64(index) - g*(uint64(1)<<c.groupingPower) + 1
	if g < 1 {
		return h - 1
	}
	return (uint64(1) << (uint64(c.groupingPower) + g - 1)) + (uint64(1)<<(g-1))*h - 1
}

// IndexToRange returns the inclusive (lower, upper) bounds for the bucket at
// index.
func (c Config) IndexToRange(index int) (uint64, uint64) {
	return c.IndexToLowerBound(index), c.IndexToUpperBound(index)
}
