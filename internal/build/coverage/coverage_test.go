package coverage

import (
	"strings"
	"testing"
)

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

// TestMeasure pins the profile reader the gate hands its own run's coverage record
// to: a number is attributed to the package its file path names, a target the profile
// never mentions measures nothing rather than zero, and a file outside the module is
// not this policy's to measure.
func TestMeasure(t *testing.T) {
	t.Parallel()

	policy := Policy{Targets: []Target{
		{Threshold: 90, Package: "cas", Tier: "core"},
		{Threshold: 80, Package: "cas/backend/fs", Tier: "backend"},
		{Threshold: 80, Package: "cas/absent", Tier: "support"},
	}}
	profile := strings.Join([]string{
		"mode: atomic",
		"example.com/mod/cas/digest.go:10.2,12.4 3 1",
		"example.com/mod/cas/digest.go:14.2,16.4 1 0",
		"example.com/mod/cas/backend/fs/fs.go:8.1,9.2 4 2",
		"example.com/other/pkg/x.go:1.1,2.2 9 9",
		"",
	}, "\n")

	results, err := policy.Measure(profile, "example.com/mod")
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	want := []Result{
		{Threshold: 90, Package: "cas", Measured: 75},
		{Threshold: 80, Package: "cas/absent", Measured: -1},
		{Threshold: 80, Package: "cas/backend/fs", Measured: 100},
	}
	if len(results) != len(want) {
		t.Fatalf("Measure = %+v, want one result per target (%d)", results, len(want))
	}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("Measure result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
}

// TestMeasureRoundsTheWayTheSuitePrints pins the one rounding the decision makes: a
// package whose statements are 89.96% covered is printed at 90.0% by the run itself,
// so a gate that judged the unrounded pair would fail a package its own log reports
// as passing.
func TestMeasureRoundsTheWayTheSuitePrints(t *testing.T) {
	t.Parallel()

	policy := Policy{Targets: []Target{{Threshold: 90, Package: "cas", Tier: "core"}}}
	// 2249 of 2500 statements is 89.96%.
	profile := "mode: set\nexample.com/mod/cas/a.go:1.1,2.2 2249 1\nexample.com/mod/cas/a.go:3.1,4.2 251 0\n"
	results, err := policy.Measure(profile, "example.com/mod")
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(results) != 1 || results[0].Measured != 90 {
		t.Fatalf("Measure = %+v, want the package measured at 90", results)
	}
	if failures := CheckResults(results); len(failures) != 0 {
		t.Errorf("CheckResults = %q, want a measurement printed as 90.0 to meet a 90 tier", failures)
	}
}

// TestMeasureReportsNothingToMeasureAsNoMeasurement pins the two shapes that are not a
// number: a target the profile never mentions, and one whose only block holds no
// statements. "Nothing to cover" is not "covered nothing", and both must reach the
// threshold check as a missing measurement rather than as a percentage.
func TestMeasureReportsNothingToMeasureAsNoMeasurement(t *testing.T) {
	t.Parallel()

	policy := Policy{Targets: []Target{{Threshold: 90, Package: "cas", Tier: "core"}}}
	for _, test := range []struct {
		name    string
		profile string
	}{
		{name: "a profile with no blocks", profile: "mode: set\n"},
		{name: "a block with no statements", profile: "mode: set\nexample.com/mod/cas/a.go:1.1,2.2 0 0\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results, err := policy.Measure(test.profile, "example.com/mod")
			if err != nil {
				t.Fatalf("Measure: %v", err)
			}
			if len(results) != 1 || results[0].HasMeasurement() {
				t.Fatalf("Measure = %+v, want one target with no measurement", results)
			}
			if failures := CheckResults(results); len(failures) != 1 {
				t.Errorf("CheckResults = %q, want the missing measurement reported", failures)
			}
		})
	}
}

// TestMeasureRefusesAProfileItCannotRead pins the two shapes that must not be read
// as a clean run: a profile that is not one, and a profile whose file paths do not
// carry the module — the second would otherwise report every target as unmeasured
// when the truth is that the reader and the run disagree.
func TestMeasureRefusesAProfileItCannotRead(t *testing.T) {
	t.Parallel()

	policy := Policy{Targets: []Target{{Threshold: 90, Package: "cas", Tier: "core"}}}
	broken := []struct {
		name    string
		profile string
	}{
		{name: "no mode line", profile: "example.com/mod/cas/a.go:1.1,2.2 1 1\n"},
		{name: "an empty profile", profile: ""},
		{name: "a line that is not a block", profile: "mode: set\nthis is not a block\n"},
		{name: "statements that are not a count", profile: "mode: set\nexample.com/mod/cas/a.go:1.1,2.2 many 1\n"},
		{name: "a negative statement count", profile: "mode: set\nexample.com/mod/cas/a.go:1.1,2.2 -1 1\n"},
		{name: "an execution count that is not a count", profile: "mode: set\nexample.com/mod/cas/a.go:1.1,2.2 1 lots\n"},
		{name: "blocks named but none inside the module", profile: "mode: set\nexample.com/other/a.go:1.1,2.2 1 1\n"},
	}
	for _, test := range broken {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.Measure(test.profile, "example.com/mod"); err == nil {
				t.Errorf("Measure(%q) succeeded, want an error", test.profile)
			}
		})
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
