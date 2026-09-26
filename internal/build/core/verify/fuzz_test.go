package verify

import (
	"strconv"
	"testing"
)

// FuzzJobs checks the reader the gate takes its concurrency from: never panics, and
// anything it accepts is an integer it can actually use. A value it accepted but could not
// use would reach the number of packages built at once, and a silent zero would leave the
// gate's longest step serial — or make `go test -p 0` fail for a reason that names nothing.
func FuzzJobs(f *testing.F) {
	f.Add("")
	f.Add("1")
	f.Add("8")
	f.Add("0")
	f.Add("007")
	f.Add(" 8")
	f.Add("+8")
	f.Add("-8")
	f.Add("999999999999999999999999999999")
	f.Add("0x8")
	f.Add("8.5")

	f.Fuzz(func(t *testing.T, value string) {
		jobs, err := Jobs(value, "VERIFY_JOBS", 4)
		if err != nil {
			if jobs != 0 {
				t.Fatalf("Jobs(%q) refused with %d, want the zero value", value, jobs)
			}
			return
		}
		if value == "" {
			// Nothing set is the caller's default, which is the one accepted value that is
			// not a rendering of its own input.
			if jobs != 4 {
				t.Fatalf("Jobs(\"\") = %d, want the caller's fallback", jobs)
			}
			return
		}
		if jobs < 1 {
			t.Fatalf("Jobs(%q) = %d, want a positive number", value, jobs)
		}
		// A value it accepted must be the value it was given, read as a decimal integer:
		// no rounding, no trimming, no reinterpretation.
		if strconv.Itoa(jobs) != value {
			t.Fatalf("Jobs(%q) = %d, which does not render back as the input", value, jobs)
		}
	})
}
