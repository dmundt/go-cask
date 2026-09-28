package coverage

import (
	"math"
	"testing"
)

// FuzzParseResult checks the two readers the gate feeds its measurements through:
// the line it writes each result as, and the coverage profile the run itself wrote.
// Neither may panic, anything ParseResult accepts carries a package and a threshold
// that is a percentage — a malformed line must be an error rather than a zero-valued
// result, which the threshold check would then silently pass — and anything Measure
// reports is a target of the policy with a percentage or no measurement at all.
func FuzzParseResult(f *testing.F) {
	f.Add("90|cas|core|91.2")
	f.Add("80|cas/backend/fs|backend|")
	f.Add("")
	f.Add("|||")
	f.Add("not-a-number|cas|core|91")
	f.Add("90|cas|core|not-a-number")
	f.Add("example.com/mod/cas/a.go:1.1,2.2 1 1")
	f.Add("example.com/mod/cas/a.go:1.1,2.2 -1 0")

	f.Fuzz(func(t *testing.T, line string) {
		result, err := ParseResult(line)
		if err == nil {
			if result.Package == "" {
				t.Fatalf("ParseResult accepted %q with no package", line)
			}
			if result.Threshold <= 0 || result.Threshold > 100 {
				t.Fatalf("ParseResult accepted %q with threshold %v", line, result.Threshold)
			}
			if math.IsNaN(result.Threshold) || math.IsInf(result.Threshold, 0) {
				t.Fatalf("ParseResult accepted %q with a non-finite threshold", line)
			}
		}
		// Whatever the line reader made of the bytes, they are also a line of the
		// run's own record: the profile reader must survive them too.
		measureProfileLine(t, line)
	})
}

// measureProfileLine drives the profile reader with bytes the line reader refused, so
// one fuzz target covers both readers of the package: whatever the profile reader
// accepts is the policy's own target, measured as a percentage or not at all.
func measureProfileLine(t *testing.T, line string) {
	t.Helper()

	policy := Policy{Targets: []Target{{Threshold: 90, Package: "cas", Tier: "core"}}}
	results, err := policy.Measure("mode: set\n"+line+"\n", "example.com/mod")
	if err != nil {
		return
	}
	if len(results) != 1 {
		t.Fatalf("Measure over %q = %d results, want one per target", line, len(results))
	}
	if got := results[0]; got.Package != "cas" || got.Measured < -1 || got.Measured > 100 {
		t.Fatalf("Measure over %q = %+v, want the target measured or unmeasured", line, got)
	}
}
