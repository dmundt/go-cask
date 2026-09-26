package gate

import (
	"strings"
	"testing"
	"time"
)

// sha01 and friends are hex object names, which a ledger line's first field has to be.
const (
	shaA = "aaa111122223333444455556666777788889999"
	shaB = "bbb111122223333444455556666777788889999"
	shaC = "ccc111122223333444455556666777788889999"
)

func TestLineAndParse(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 25, 14, 46, 49, 0, time.UTC)
	line := Line(shaA, "full", at)
	if line != shaA+" full 2026-09-25T14:46:49Z" {
		t.Errorf("Line = %q, want the three-field RFC 3339 form", line)
	}
	entry, ok := Parse(line)
	if !ok || entry.SHA != shaA || entry.Scope != "full" || !entry.At.Equal(at) {
		t.Errorf("Parse(%q) = %+v, %v", line, entry, ok)
	}

	// A line that names no commit and no scope is not an entry, so a stray line can
	// never be read as a verification.
	for _, line := range []string{"", "   ", "abc123", "# a comment", "not a commit", "aaaaaaa"} {
		if _, ok := Parse(line); ok {
			t.Errorf("Parse(%q) accepted a line that names no verification", line)
		}
	}
	// A line without a readable stamp is still an entry: the commit was verified, and
	// refusing to read it would refuse a push that is authorised.
	entry, ok = Parse(shaA + " full not-a-time")
	if !ok || entry.SHA != shaA || !entry.At.IsZero() {
		t.Errorf("Parse without a stamp = %+v, %v; want the entry with no time", entry, ok)
	}
}

// TestVerified pins what the pre-push hook asks: does ANY entry name this commit. One
// line per clone used to invalidate every other branch's stamp, so the ledger is a set.
func TestVerified(t *testing.T) {
	t.Parallel()

	ledger := Line(shaA, "docs", time.Unix(0, 0)) + "\n" + Line(shaB, "full", time.Unix(0, 0)) + "\n"
	cases := []struct {
		name string
		sha  string
		want bool
	}{
		{name: "an entry for the commit", sha: shaB, want: true},
		{name: "another worktree's entry does not carry this commit", sha: shaC},
		{name: "a prefix of the commit is not the commit", sha: shaB[:8]},
		{name: "no commit asked for", sha: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Verified(ledger, tc.sha); got != tc.want {
				t.Errorf("Verified(%q) = %v, want %v", tc.sha, got, tc.want)
			}
		})
	}
}

// TestAppendIsOneLinePerCommit is the writer contract the gate relies on: it replaces
// this commit's line, preserves every other entry, and keeps the ledger bounded by
// holding the newest entries.
func TestAppendIsOneLinePerCommit(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 25, 14, 46, 49, 0, time.UTC)
	ledger := Line(shaA, "docs", time.Unix(0, 0)) + "\n" + Line(shaB, "docs", time.Unix(1, 0)) + "\n"

	updated := Append(ledger, shaB, "full", at, 200)
	if strings.Count(updated, shaB+" ") != 1 {
		t.Errorf("the ledger holds %d entries for one commit:\n%s", strings.Count(updated, shaB+" "), updated)
	}
	if !strings.Contains(updated, Line(shaA, "docs", time.Unix(0, 0))) {
		t.Errorf("the append dropped another worktree's entry:\n%s", updated)
	}
	if !strings.Contains(updated, Line(shaB, "full", at)) {
		t.Errorf("the append did not write the new entry:\n%s", updated)
	}
	if !strings.HasSuffix(updated, "\n") {
		t.Errorf("the ledger does not end in a newline: %q", updated)
	}

	// A ledger longer than the bound keeps the newest entries and drops the oldest.
	var long strings.Builder
	for i := 0; i < 250; i++ {
		long.WriteString(Line(shaN(i), "full", at) + "\n")
	}
	bounded := Append(long.String(), shaN(999), "full", at, 200)
	if got := len(lines(bounded)); got != 200 {
		t.Errorf("the bounded ledger holds %d entries, want 200", got)
	}
	if !Verified(bounded, shaN(999)) {
		t.Error("the bounded ledger dropped the entry it just wrote")
	}
	if Verified(bounded, shaN(0)) {
		t.Error("the bounded ledger kept the oldest entry instead of dropping it")
	}
}

// TestAppendReadsAnotherToolchainLedger pins the carriage return: the two toolchains that
// work in this repository write the same ledger, and a line ending in CR must still be an
// entry rather than an unrecognised one.
func TestAppendReadsAnotherToolchainLedger(t *testing.T) {
	t.Parallel()

	ledger := Line(shaA, "docs", time.Unix(0, 0)) + "\r\n"
	if !Verified(ledger, shaA) {
		t.Error("a ledger written with CRLF endings does not verify its commit")
	}
	updated := Append(ledger, shaB, "full", time.Unix(0, 0), 200)
	if strings.Contains(updated, "\r") {
		t.Errorf("the writer carried the carriage return through: %q", updated)
	}
	if !Verified(updated, shaA) {
		t.Error("the writer dropped the entry it did not own")
	}
}

// shaN renders a stable 40-character hex stand-in for a commit hash at one index.
func shaN(n int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 40)
	for i := range out {
		out[i] = digits[(n>>uint(i%8))&0xf]
	}
	return string(out)
}
