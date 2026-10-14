package functions

import "math"

func randomSeedWords(value float64) (uint64, uint64) {
	if value == 0 {
		value = 0
	}
	bits := math.Float64bits(value)
	return bits ^ 0x9e3779b97f4a7c15, bits ^ 0xd1b54a32d192ed03
}

type randomIntegerSource interface{ Uint64() uint64 }

// Rejection sampling keeps every integer in the inclusive range equiprobable.
func randomInteger[S randomIntegerSource](lower, upper int64, source S) int64 {
	if lower == upper {
		return lower
	}
	span := uint64(upper) - uint64(lower) + 1
	if span == 0 {
		return int64(uint64(lower) + source.Uint64())
	}
	threshold := -span % span
	for {
		value := source.Uint64()
		if value >= threshold {
			return int64(uint64(lower) + value%span)
		}
	}
}
