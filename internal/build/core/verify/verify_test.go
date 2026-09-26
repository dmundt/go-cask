package verify

import (
	"strings"
	"testing"
)

// TestRequested pins the automatic scope, both assertions and the refusal: the change set
// decides only when nothing was asked for, an asserted documentation scope fails on a code
// change rather than quietly running the whole gate, and a scope that is neither is the
// caller's mistake.
func TestRequested(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		value    string
		docsOnly bool
		want     Scope
		wantErr  string
	}{
		{name: "nothing asked, a code change", value: "", want: Full},
		{name: "nothing asked, a documentation change", value: "", docsOnly: true, want: Docs},
		{name: "auto on a code change", value: "auto", want: Full},
		{name: "auto on a documentation change", value: "auto", docsOnly: true, want: Docs},
		{name: "the whole gate forced", value: "full", want: Full},
		{name: "the whole gate forced on a documentation change", value: "full", docsOnly: true, want: Full},
		{name: "the documentation scope asserted", value: "docs", docsOnly: true, want: Docs},
		{
			// The assertion is the point of the value: a caller that asked for the cheap
			// scope must be told the tree is not documentation-only, not handed the whole
			// gate or a green run.
			name:  "the documentation scope asserted on a code change",
			value: "docs", want: Full, wantErr: "not documentation-only",
		},
		{name: "neither", value: "fast", want: Full, wantErr: "must be auto, docs or full"},
		{name: "the empty-looking value", value: " ", want: Full, wantErr: "must be auto, docs or full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scope, err := Requested(tc.value, "VERIFY_SCOPE", tc.docsOnly)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Requested(%q, docsOnly=%v) = %v, %v; want an error naming %q",
						tc.value, tc.docsOnly, scope, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Requested(%q, docsOnly=%v): %v", tc.value, tc.docsOnly, err)
			}
			if scope != tc.want {
				t.Errorf("Requested(%q, docsOnly=%v) = %v, want %v", tc.value, tc.docsOnly, scope, tc.want)
			}
		})
	}
}

// TestScopeString pins the two spellings, because one of them is written into the ledger
// the pre-push hook reads.
func TestScopeString(t *testing.T) {
	t.Parallel()

	if got := Full.String(); got != "full" {
		t.Errorf("Full.String() = %q, want %q", got, "full")
	}
	if got := Docs.String(); got != "docs" {
		t.Errorf("Docs.String() = %q, want %q", got, "docs")
	}
}

// TestJobs pins the concurrency reader: the caller's default when nothing is set, and a
// decimal integer and nothing else when something is — every shape strconv would have
// accepted on its own is refused, because a concurrency the caller did not mean silently
// serializes the gate's longest step.
func TestJobs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value   string
		want    int
		wantErr bool
	}{
		{value: "", want: 20},
		{value: "1", want: 1},
		{value: "8", want: 8},
		{value: "999", want: 999},
		{value: "0", wantErr: true},
		{value: "00", wantErr: true},
		{value: "007", wantErr: true},
		{value: " 8", wantErr: true},
		{value: "8 ", wantErr: true},
		{value: "+8", wantErr: true},
		{value: "-8", wantErr: true},
		{value: "1_000", wantErr: true},
		{value: "0x8", wantErr: true},
		{value: "8.0", wantErr: true},
		{value: "eight", wantErr: true},
		{value: strings.Repeat("9", 40), wantErr: true},
	}
	for _, tc := range cases {
		value := tc.value
		t.Run("value="+value, func(t *testing.T) {
			t.Parallel()
			jobs, err := Jobs(value, "VERIFY_JOBS", 20)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Jobs(%q) = %d, want an error", value, jobs)
				}
				if !strings.Contains(err.Error(), "must be a positive integer") {
					t.Errorf("Jobs(%q) refused with %q", value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Jobs(%q): %v", value, err)
			}
			if jobs != tc.want {
				t.Errorf("Jobs(%q) = %d, want %d", value, jobs, tc.want)
			}
		})
	}
}

// TestEscape pins the one comparison that decides whether a run may be recorded: only the
// exact value the gate reads as set counts, and the drop-everything switch wins whatever
// the individual value says.
func TestEscape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value   string
		dropAll bool
		want    bool
	}{
		{value: "", want: false},
		{value: "true", want: true},
		{value: "TRUE", want: false},
		{value: "True", want: false},
		{value: "1", want: false},
		{value: "yes", want: false},
		{value: "false", want: false},
		{value: " true", want: false},
		{value: "", dropAll: true, want: true},
		{value: "false", dropAll: true, want: true},
		{value: "true", dropAll: true, want: true},
	}
	for _, tc := range cases {
		if got := Escape(tc.value, tc.dropAll); got != tc.want {
			t.Errorf("Escape(%q, dropAll=%v) = %v, want %v", tc.value, tc.dropAll, got, tc.want)
		}
	}
}
