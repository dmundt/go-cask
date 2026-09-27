package receipt

import (
	"strings"
	"testing"
)

// recordFixture is a complete receipt, as Render writes it.
func recordFixture() Record {
	return Record{
		Commit:        "aaaa1111",
		Tree:          "bbbb2222",
		Base:          "cccc3333",
		Diff:          "dddd4444",
		Scope:         Full,
		Checks:        []string{"gofmt", "go-test-race"},
		CoverageTiers: "38",
		Go:            "go1.27.1",
		Runner:        "linux/x86_64",
		Run:           "2026-09-27T11:00:00Z",
	}
}

// TestRenderThenParseIsTheSameRecord pins the format's round trip, and with it the order
// Render writes: a reader diffs two receipts, so the fields may not move around.
func TestRenderThenParseIsTheSameRecord(t *testing.T) {
	t.Parallel()

	record := recordFixture()
	rendered := Render(record)
	if !strings.HasPrefix(rendered, Version+"\n") {
		t.Errorf("Render did not start with the version line:\n%s", rendered)
	}
	parsed, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse(Render(record)): %v", err)
	}
	if parsed.Commit != record.Commit || parsed.Tree != record.Tree || parsed.Base != record.Base {
		t.Errorf("the commit fields came back as %+v", parsed)
	}
	if parsed.Diff != record.Diff || parsed.Scope != record.Scope {
		t.Errorf("the change fields came back as %+v", parsed)
	}
	if strings.Join(parsed.Checks, ",") != strings.Join(record.Checks, ",") {
		t.Errorf("the checks came back as %q, want %q", parsed.Checks, record.Checks)
	}
	if parsed.CoverageTiers != record.CoverageTiers || parsed.Go != record.Go ||
		parsed.Runner != record.Runner || parsed.Run != record.Run {
		t.Errorf("the run fields came back as %+v", parsed)
	}

	// The field order itself is the contract, so it is pinned rather than implied.
	wantOrder := []string{"commit", "tree", "base", "diff", "scope", "check", "check", "coverage-tiers", "go", "runner", "run"}
	names := make([]string, 0, len(wantOrder))
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")[1:] {
		name, _, _ := strings.Cut(line, " ")
		names = append(names, name)
	}
	if strings.Join(names, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("Render wrote the fields as %v, want %v", names, wantOrder)
	}
}

// TestParseIsPermissiveAboutFieldsAndStrictAboutTheVersion pins what a reader may accept.
// The version line is the format, so a record without it is refused; a field it does not
// know is a later writer's and is ignored; a repeated field keeps its first value, which is
// what a verification compares.
func TestParseIsPermissiveAboutFieldsAndStrictAboutTheVersion(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{"", "cask-gate-receipt 2\ncommit a\n", "commit a\n", "cask-gate-receipt 1 extra\n"} {
		if _, err := Parse(payload); err == nil {
			t.Errorf("Parse(%q) accepted a payload that is not the format", payload)
		}
	}

	parsed, err := Parse(Version + "\n" +
		"commit first\n" +
		"commit second\n" +
		"future-field whatever\n" +
		"a line with no value\n" +
		"scope full\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Commit != "first" {
		t.Errorf("a repeated field kept %q, want the first value", parsed.Commit)
	}
	if parsed.Scope != Full {
		t.Errorf("scope parsed as %q", parsed.Scope)
	}

	// A receipt without the optional count, and with no checks at all, is still a receipt:
	// a docs-scope run measures no coverage.
	sparse, err := Parse(Version + "\ncommit a\ntree b\nbase c\ndiff d\nscope docs\ngo x\nrunner y\nrun z\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sparse.Checks) != 0 || sparse.CoverageTiers != "" {
		t.Errorf("the sparse receipt parsed as %+v", sparse)
	}
}

// TestParseScope pins the two tokens and refuses everything else: a scope is compared, not
// interpreted, so a near-miss is a broken record.
func TestParseScope(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		want  Scope
		ok    bool
	}{
		{value: "docs", want: Docs, ok: true},
		{value: "full", want: Full, ok: true},
		{value: "", ok: false},
		{value: "Docs", ok: false},
		{value: "documentation", ok: false},
	} {
		scope, err := ParseScope(tc.value)
		if tc.ok && (err != nil || scope != tc.want) {
			t.Errorf("ParseScope(%q) = %q, %v; want %q", tc.value, scope, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseScope(%q) = %q, want a refusal", tc.value, scope)
		}
	}
}

// TestCheckName pins the character set: a name is one token on one line, so a space or a
// newline in it does not make the receipt wrong — it makes it unreadable.
func TestCheckName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"gofmt", "go-test-race", "coverage-tiers", "website.examples", "a_b"} {
		if err := CheckName(name); err != nil {
			t.Errorf("CheckName(%q) refused a name the format carries: %v", name, err)
		}
	}
	for _, name := range []string{"", "two words", "line\nbreak", "tab\there", "semi;colon", "sla/sh"} {
		if err := CheckName(name); err == nil {
			t.Errorf("CheckName(%q) accepted a name that cannot ride in the format", name)
		}
	}
}

// TestIdentityIsTheEvidenceWithoutTheRun pins the comparison a republish is decided by: two
// receipts for one commit that agree on what was covered are the same evidence, however
// often the gate ran and on what host.
func TestIdentityIsTheEvidenceWithoutTheRun(t *testing.T) {
	t.Parallel()

	first := recordFixture()
	second := first
	second.Go = "go1.27.2"
	second.Runner = "windows/x86_64"
	second.Run = "2026-09-27T23:59:59Z"
	if first.Identity() != second.Identity() {
		t.Errorf("two runs of one gate differ in identity:\n%s\n%s", first.Identity(), second.Identity())
	}

	// Every field above the run line is evidence, so a change to any of them moves the
	// identity — that is what makes the ref replace itself when the evidence moved.
	moved := []Record{first, first, first, first, first, first, first}
	moved[0].Commit = "other"
	moved[1].Tree = "other"
	moved[2].Base = "other"
	moved[3].Diff = "other"
	moved[4].Scope = Docs
	moved[5].Checks = []string{"gofmt"}
	moved[6].CoverageTiers = "37"
	for i, record := range moved {
		if record.Identity() == first.Identity() {
			t.Errorf("change %d did not move the identity:\n%s", i, record.Identity())
		}
	}

	// A receipt with no run measured says so with an empty count rather than a zero.
	withoutTiers := first
	withoutTiers.CoverageTiers = ""
	if withoutTiers.Identity() == first.Identity() {
		t.Error("a missing coverage count did not move the identity")
	}
	if strings.Contains(withoutTiers.Identity(), "coverage-tiers") {
		t.Errorf("an empty count was written as a field:\n%s", withoutTiers.Identity())
	}
}

// TestCanonicalPaths pins the bytes both sides of a verification hash: sorted, one per line,
// empty entries dropped, and a trailing newline only when there is something to terminate.
func TestCanonicalPaths(t *testing.T) {
	t.Parallel()

	if got := CanonicalPaths(nil); got != "" {
		t.Errorf("CanonicalPaths(nil) = %q, want the empty string", got)
	}
	if got := CanonicalPaths([]string{"", "  "}); got != "" {
		t.Errorf("CanonicalPaths of blanks = %q, want the empty string", got)
	}

	got := CanonicalPaths([]string{"internal/build/z.go", "cas/store.go", "", "docs/index.md"})
	want := "cas/store.go\ndocs/index.md\ninternal/build/z.go\n"
	if got != want {
		t.Errorf("CanonicalPaths = %q, want %q", got, want)
	}
	// The order it is given must not matter, or the same change would hash two ways.
	reordered := CanonicalPaths([]string{"docs/index.md", "internal/build/z.go", "cas/store.go"})
	if reordered != want {
		t.Errorf("CanonicalPaths depends on the input order: %q", reordered)
	}
}
