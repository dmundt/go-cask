package standard

import (
	"strings"
	"testing"
)

// TestNewFilterReportsAnUnusableBitCount pins the constructor's propagation of
// bloom.Parameters' rejection: a request whose computed bit count is not a
// usable size is reported with this package named, instead of allocating a
// bitmap the formula could not size.
func TestNewFilterReportsAnUnusableBitCount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expectedItems uint64
		rate          float64
	}{
		{"an absurd item count", uint64(1) << 62, 0.5},
		{"a denormal false-positive rate", uint64(1) << 62, 5e-324},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NewFilter(Config{ExpectedItems: tc.expectedItems, FalsePositiveRate: tc.rate})
			if err == nil {
				t.Fatalf("NewFilter(%d items, rate %g) = %v, want the bit-count error", tc.expectedItems, tc.rate, f)
			}
			if !strings.Contains(err.Error(), "bloom/standard") {
				t.Fatalf("NewFilter(%d items, rate %g) = %v, want it to name the package", tc.expectedItems, tc.rate, err)
			}
			if !strings.Contains(err.Error(), "above the") && !strings.Contains(err.Error(), "invalid bit count") {
				t.Fatalf("NewFilter(%d items, rate %g) = %v, want the bit-count rejection", tc.expectedItems, tc.rate, err)
			}
			if f != nil {
				t.Fatal("a rejected configuration still returned a filter")
			}
		})
	}
}
