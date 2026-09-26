package coverage

import (
	"os/exec"
	"strings"
	"testing"
)

// modulePath reads the module path, so a package can be named by its import path
// rather than a "./"-relative one: go list resolves an import path from any
// working directory, while a relative path is resolved against the test's own
// directory and would point at nothing.
func modulePath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Path}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		t.Fatal("go list -m reported an empty module path")
	}
	return path
}

// casPackages asks Go for the packages under cas/, which is the tree the policy
// is total over. The test needs the real list: the whole point of the drift check
// is to compare the policy against what the module actually contains.
func casPackages(t *testing.T) []string {
	t.Helper()
	pattern := modulePath(t) + "/cas/..."
	out, err := exec.Command("go", "list", pattern).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			packages = append(packages, line)
		}
	}
	if len(packages) == 0 {
		t.Fatalf("go list %s reported no packages, so this test would prove nothing", pattern)
	}
	return packages
}

// TestStripModule pins the boundary between Go's import paths and the policy's
// module-relative ones: getting it wrong reports every package as uncovered, and
// a package outside the module is a caller error rather than a silent omission.
func TestStripModule(t *testing.T) {
	t.Parallel()

	module := "example.com/mod"
	tests := []struct {
		name       string
		discovered []string
		want       []string
		wantErr    bool
	}{
		{
			name:       "paths inside the module are made relative",
			discovered: []string{module + "/cas", module + "/cas/backend/fs"},
			want:       []string{"cas", "cas/backend/fs"},
		},
		{
			name:       "empty entries are dropped",
			discovered: []string{"", module + "/cas"},
			want:       []string{"cas"},
		},
		{
			name:       "the module root keeps its full path",
			discovered: []string{module},
			want:       []string{module},
		},
		{
			name:       "a package outside the module is an error",
			discovered: []string{module + "/cas", "example.com/other/x"},
			wantErr:    true,
		},
		{
			name:       "a sibling module with a shared prefix is an error",
			discovered: []string{"example.com/modular/x"},
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := StripModule(module, test.discovered)
			if test.wantErr {
				if err == nil {
					t.Fatalf("StripModule(%q) succeeded, want an error", test.discovered)
				}
				return
			}
			if err != nil {
				t.Fatalf("StripModule(%q): %v", test.discovered, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("StripModule(%q) = %v, want %v", test.discovered, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("StripModule(%q) = %v, want %v", test.discovered, got, test.want)
				}
			}
		})
	}
}

// TestUncovered pins the decision on constructed lists, including the forms that
// would otherwise slip through: a "./"-prefixed discovery, a duplicate, an empty
// entry, and a package gated under a different spelling than Go reports.
func TestUncovered(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Targets: []Target{
			{Threshold: 90, Package: "cas", Tier: "core"},
			{Threshold: 80, Package: "./cas/cache", Tier: "support"},
		},
		Exempt: []Exemption{
			{Package: "cas/legacy", Reason: "kept for one release while callers migrate"},
		},
	}

	tests := []struct {
		name       string
		discovered []string
		want       []string
	}{
		{
			name:       "everything covered",
			discovered: []string{"cas", "cas/cache", "cas/legacy"},
			want:       []string{},
		},
		{
			name:       "a go-list prefixed discovery still matches",
			discovered: []string{"./cas", "./cas/cache"},
			want:       []string{},
		},
		{
			name:       "a new package is named",
			discovered: []string{"cas", "cas/cache", "cas/newthing"},
			want:       []string{"cas/newthing"},
		},
		{
			name:       "exempt packages count as covered",
			discovered: []string{"cas/legacy"},
			want:       []string{},
		},
		{
			name:       "duplicates are reported once",
			discovered: []string{"cas/newthing", "cas/newthing", "./cas/newthing"},
			want:       []string{"cas/newthing"},
		},
		{
			name:       "empty entries are ignored",
			discovered: []string{"", "cas"},
			want:       []string{},
		},
		{
			name:       "nothing discovered means nothing missing",
			discovered: nil,
			want:       []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.Uncovered(test.discovered)
			if err != nil {
				t.Fatalf("Uncovered(%q): %v", test.discovered, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("Uncovered(%q) = %v, want %v", test.discovered, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("Uncovered(%q) = %v, want %v", test.discovered, got, test.want)
				}
			}
		})
	}
}

// TestUncoveredReportsAMalformedPolicy pins that a broken table fails loudly: a
// policy that cannot be read must not be treated as covering nothing, which would
// report every package as uncovered, or as covering everything, which would pass
// silently.
func TestUncoveredReportsAMalformedPolicy(t *testing.T) {
	t.Parallel()

	broken := []struct {
		name   string
		policy Policy
	}{
		{
			name:   "a target with no package",
			policy: Policy{Targets: []Target{{Threshold: 90, Tier: "core"}}},
		},
		{
			name:   "a target with no tier",
			policy: Policy{Targets: []Target{{Threshold: 90, Package: "cas"}}},
		},
		{
			name:   "a threshold that is not a percentage",
			policy: Policy{Targets: []Target{{Threshold: 140, Package: "cas", Tier: "core"}}},
		},
		{
			name:   "a threshold of zero",
			policy: Policy{Targets: []Target{{Threshold: 0, Package: "cas", Tier: "core"}}},
		},
		{
			name: "a package listed twice",
			policy: Policy{Targets: []Target{
				{Threshold: 90, Package: "cas", Tier: "core"},
				{Threshold: 80, Package: "cas", Tier: "other"},
			}},
		},
		{
			name:   "an exemption with no reason",
			policy: Policy{Exempt: []Exemption{{Package: "cas/x"}}},
		},
		{
			name: "a package both gated and exempt",
			policy: Policy{
				Targets: []Target{{Threshold: 90, Package: "cas", Tier: "core"}},
				Exempt:  []Exemption{{Package: "cas", Reason: "contradiction"}},
			},
		},
	}

	for _, test := range broken {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.policy.Uncovered([]string{"cas"}); err == nil {
				t.Error("a malformed policy was accepted, want an error")
			}
		})
	}
}

// TestMeets pins the threshold comparison, including the boundary: the gate's
// documented contract is "at least the threshold", so a measurement exactly on the
// number passes and a hair below fails.
func TestMeets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		measured  float64
		threshold float64
		want      bool
	}{
		{name: "above the threshold", measured: 91.5, threshold: 90, want: true},
		{name: "exactly the threshold", measured: 90, threshold: 90, want: true},
		{name: "just below the threshold", measured: 89.9, threshold: 90, want: false},
		{name: "well below the threshold", measured: 12, threshold: 80, want: false},
		{name: "zero coverage of a zero threshold", measured: 0, threshold: 0, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Meets(test.measured, test.threshold); got != test.want {
				t.Errorf("Meets(%v, %v) = %v, want %v", test.measured, test.threshold, got, test.want)
			}
		})
	}
}

// TestParseResult pins the measurement format the gate writes, including the
// empty-measurement case: a run that printed no coverage line is a failure, not a
// zero.
func TestParseResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		line    string
		want    Result
		wantErr bool
	}{
		{
			name: "a measurement above the threshold",
			line: "90|./cas|99.8",
			want: Result{Threshold: 90, Package: "./cas", Measured: 99.8},
		},
		{
			name: "a measurement exactly on the threshold",
			line: "80|./gitlike|80",
			want: Result{Threshold: 80, Package: "./gitlike", Measured: 80},
		},
		{
			name: "an empty measurement means no coverage was reported",
			line: "90|./cas|",
			want: Result{Threshold: 90, Package: "./cas", Measured: -1},
		},
		{
			name: "a whitespace measurement is also absent",
			line: "90|./cas|  ",
			want: Result{Threshold: 90, Package: "./cas", Measured: -1},
		},
		{
			name:    "too few fields",
			line:    "90|./cas",
			wantErr: true,
		},
		{
			name:    "no package",
			line:    "90||99.8",
			wantErr: true,
		},
		{
			name:    "a threshold that is not a number",
			line:    "ninety|./cas|99.8",
			wantErr: true,
		},
		{
			name:    "a measurement that is not a number",
			line:    "90|./cas|lots",
			wantErr: true,
		},
		{
			name:    "a measurement outside a percentage range",
			line:    "90|./cas|140",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseResult(test.line)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseResult(%q) succeeded, want an error", test.line)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseResult(%q): %v", test.line, err)
			}
			if got != test.want {
				t.Errorf("ParseResult(%q) = %+v, want %+v", test.line, got, test.want)
			}
		})
	}
}

// TestCheckResults pins the two failures the gate reports: a package below its
// tier, and a package whose run produced no coverage at all.
func TestCheckResults(t *testing.T) {
	t.Parallel()

	results := []Result{
		{Threshold: 90, Package: "./cas", Measured: 99.8},
		{Threshold: 90, Package: "./cas/backend", Measured: 90},
		{Threshold: 80, Package: "./gitlike", Measured: 79.9},
		{Threshold: 80, Package: "./cas/pack", Measured: -1},
	}
	failures := CheckResults(results)
	if len(failures) != 2 {
		t.Fatalf("CheckResults = %q, want 2 failures", failures)
	}
	if !strings.Contains(failures[0], "79.9% below 80% for ./gitlike") {
		t.Errorf("first failure = %q, want the below-threshold package", failures[0])
	}
	if !strings.Contains(failures[1], "coverage output missing for ./cas/pack") {
		t.Errorf("second failure = %q, want the missing measurement", failures[1])
	}
}

// TestCheckResultsPassesAtTheBoundary pins that a measurement exactly on the tier
// passes, which is the difference between the gate's contract and "below".
func TestCheckResultsPassesAtTheBoundary(t *testing.T) {
	t.Parallel()

	if failures := CheckResults([]Result{
		{Threshold: 90, Package: "./cas", Measured: 90},
		{Threshold: 80, Package: "./gitlike", Measured: 80.0},
	}); len(failures) != 0 {
		t.Errorf("CheckResults = %q, want no failures at the boundary", failures)
	}
}

// TestParseThreshold pins the table's own parser: the gate reads a number from
// coverage output, so a threshold that is not a number has to be an error rather
// than a comparison that quietly evaluates false.
func TestParseThreshold(t *testing.T) {
	t.Parallel()

	good := []struct {
		text string
		want float64
	}{
		{text: "90", want: 90},
		{text: "80", want: 80},
		{text: " 85.5 ", want: 85.5},
		{text: "100", want: 100},
	}
	for _, test := range good {
		got, err := ParseThreshold(test.text)
		if err != nil {
			t.Errorf("ParseThreshold(%q) returned %v, want %v", test.text, err, test.want)
			continue
		}
		if got != test.want {
			t.Errorf("ParseThreshold(%q) = %v, want %v", test.text, got, test.want)
		}
	}

	bad := []string{"", "ninety", "0", "-5", "140"}
	for _, text := range bad {
		if _, err := ParseThreshold(text); err == nil {
			t.Errorf("ParseThreshold(%q) succeeded, want an error", text)
		}
	}
}
