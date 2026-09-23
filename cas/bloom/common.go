package bloom

import (
	"fmt"
	"math"
)

// MaxBits is the upper bound Parameters places on a filter's bit count.
//
// It exists so a caller mistake cannot turn into an unbounded allocation: the
// formula below grows linearly with expectedItems, so an implausible count such
// as 4e15 would otherwise ask for a multi-petabyte bitmap and the constructor
// would panic with "len out of range" instead of reporting an error. At the
// ceiling a standard or persistent bitmap occupies MaxBits/8 (512 MiB), and a
// counting filter occupies MaxBits*4 because it stores one 32-bit counter per
// bit. Callers that need a larger filter than MaxBits allows must shard the key
// space across several filters (or relax the false-positive rate) rather than
// raise the ceiling.
const MaxBits uint64 = 1 << 32

// ValidateFalsePositiveRate ensures the configured Bloom false-positive rate is
// within the valid open interval (0, 1). NaN is rejected as well: every
// comparison against it is false, so it would otherwise slip through the
// interval check and poison the bit-count formula.
func ValidateFalsePositiveRate(rate float64, name string) error {
	if math.IsNaN(rate) || rate <= 0 || rate >= 1 {
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

// Parameters computes the approximate bit count m and probe count k needed for
// a Bloom filter configured to hold expectedItems entries at falsePositiveRate.
//
// An expected item count of zero is not an error: it yields the minimal (1, 1)
// filter so callers can defer sizing.
//
// Parameters reports an error rather than returning a useless bitmap when the
// request cannot be honoured:
//
//   - falsePositiveRate outside the open interval (0, 1), including NaN;
//   - a computed bit count that is not a finite positive number;
//   - a computed bit count above MaxBits.
//
// The last case is the guard that matters: the bit count grows linearly with
// expectedItems, so an unrealistic count such as 4e15 would otherwise produce a
// multi-petabyte size and a "len out of range" panic inside the caller's
// allocation. A caller that hits the ceiling should shard the key space across
// several filters or accept a coarser false-positive rate; raising the ceiling
// is not a supported answer.
func Parameters(expectedItems uint64, falsePositiveRate float64) (m uint64, k int, err error) {
	if math.IsNaN(falsePositiveRate) || falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return 0, 0, fmt.Errorf("false positive rate %v must be in (0, 1)", falsePositiveRate)
	}
	if expectedItems == 0 {
		return 1, 1, nil
	}
	bits := -float64(expectedItems) * math.Log(falsePositiveRate) / (math.Log(2) * math.Log(2))
	if math.IsNaN(bits) || math.IsInf(bits, 0) || bits <= 0 {
		return 0, 0, fmt.Errorf("expected items %d at false positive rate %v yields an invalid bit count", expectedItems, falsePositiveRate)
	}
	if bits > float64(MaxBits) {
		return 0, 0, fmt.Errorf("expected items %d at false positive rate %v needs %.0f bits, above the %d-bit limit; shard the key space or use a coarser rate", expectedItems, falsePositiveRate, bits, MaxBits)
	}
	m = uint64(bits)
	if m == 0 {
		m = 1
	}
	k = int(math.Round((float64(m) / float64(expectedItems)) * math.Log(2)))
	if k <= 0 {
		k = 1
	}
	return m, k, nil
}

// Indices computes the Bloom bit positions for data using the supplied index
// function and filter dimensions.
func Indices(hash IndexHash, data []byte, k int, m uint64) []uint64 {
	positions := make([]uint64, 0, k)
	for i := range k {
		positions = append(positions, hash(data, i)%m)
	}
	return positions
}
