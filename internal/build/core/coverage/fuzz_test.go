package coverage

import (
	"math"
	"testing"
)

// FuzzParseResult checks the line reader the gate feeds its measurements through: never
// panics, and anything it accepts carries a package, a tier and a threshold that is a
// percentage — a malformed line must be an error rather than a zero-valued result, which
// the threshold check would then silently pass.
func FuzzParseResult(f *testing.F) {
	f.Add("90|cas|core|91.2")
	f.Add("80|cas/backend/fs|backend|")
	f.Add("")
	f.Add("|||")
	f.Add("not-a-number|cas|core|91")
	f.Add("90|cas|core|not-a-number")

	f.Fuzz(func(t *testing.T, line string) {
		result, err := ParseResult(line)
		if err != nil {
			return
		}
		if result.Package == "" {
			t.Fatalf("ParseResult accepted %q with no package", line)
		}
		if result.Threshold <= 0 || result.Threshold > 100 {
			t.Fatalf("ParseResult accepted %q with threshold %v", line, result.Threshold)
		}
		if math.IsNaN(result.Threshold) || math.IsInf(result.Threshold, 0) {
			t.Fatalf("ParseResult accepted %q with a non-finite threshold", line)
		}
	})
}
