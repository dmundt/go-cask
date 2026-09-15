package bloom

import (
	"fmt"
	"math"
)

// ValidateFalsePositiveRate ensures the configured Bloom false-positive rate is
// within the valid open interval (0, 1).
func ValidateFalsePositiveRate(rate float64, name string) error {
	if rate <= 0 || rate >= 1 {
		return fmt.Errorf("%s: false positive rate must be in (0, 1)", name)
	}
	return nil
}

// ResolveIndexHash returns the caller's Bloom index function or the shared
// default when no custom hash function is supplied.
func ResolveIndexHash(hash IndexHash) IndexHash {
	if hash == nil {
		return DefaultIndexHash
	}
	return hash
}

// Parameters computes the approximate bit count and probe count needed for a
// Bloom filter configured to hold expectedItems entries at falsePositiveRate.
func Parameters(expectedItems uint64, falsePositiveRate float64) (m uint64, k int) {
	if expectedItems == 0 {
		return 1, 1
	}
	m = uint64(-float64(expectedItems) * math.Log(falsePositiveRate) / (math.Log(2) * math.Log(2)))
	if m == 0 {
		m = 1
	}
	k = int(math.Round((float64(m) / float64(expectedItems)) * math.Log(2)))
	if k <= 0 {
		k = 1
	}
	return m, k
}

// Indices computes the Bloom bit positions for data using the supplied index
// function and filter dimensions.
func Indices(hash IndexHash, data []byte, k int, m uint64) []uint64 {
	positions := make([]uint64, 0, k)
	for i := 0; i < k; i++ {
		positions = append(positions, hash(data, i)%m)
	}
	return positions
}
