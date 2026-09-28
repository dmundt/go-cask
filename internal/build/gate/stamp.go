// Package gate owns the gate stamp: the ledger that records which commits ran the gate
// green, and the two questions asked of it — may this commit be pushed, and how is the
// ledger written back.
//
// The ledger is append-only and holds one line per verified commit, which is deliberate.
// It used to hold a single line, so a gate run in any other worktree of one clone
// silently invalidated every other branch's stamp and refused its push. A stamp is
// therefore per commit — re-pushing an unchanged commit costs no compute — and a run
// that skipped a step writes nothing at all, so the file means "the whole gate ran green
// on this commit" and nothing weaker.
package gate

import (
	"strings"
	"time"
)

// StampLayout is the timestamp a ledger line carries: RFC 3339 in UTC, so the entries
// sort as text and name no local zone.
const StampLayout = time.RFC3339

// Entry is one verified commit: the commit, the scope its run covered, and when.
type Entry struct {
	// SHA is the full commit hash the run verified.
	SHA string
	// Scope is what the run covered, e.g. "docs" or "full".
	Scope string
	// At is when the run finished.
	At time.Time
}

// Line renders one ledger line: three space-separated fields.
func Line(sha, scope string, at time.Time) string {
	return sha + " " + scope + " " + at.UTC().Format(StampLayout)
}

// Parse reads one ledger line. A line whose first field is not a hex object name, or
// that names no scope, is not an entry: a comment, a blank line or a stray word must
// never be read as a verification of anything.
func Parse(line string) (Entry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !isObjectName(fields[0]) {
		return Entry{}, false
	}
	entry := Entry{SHA: fields[0], Scope: fields[1]}
	if len(fields) > 2 {
		if at, err := time.Parse(StampLayout, fields[2]); err == nil {
			entry.At = at
		}
	}
	return entry, true
}

// isObjectName reports whether a field looks like a Git object name: lowercase hex,
// long enough not to be a word. It is what keeps a stray line out of the ledger's
// meaning without giving the file a syntax to parse.
func isObjectName(field string) bool {
	if len(field) < 7 {
		return false
	}
	for _, digit := range field {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return false
		}
	}
	return true
}

// Verified reports whether the ledger carries an entry for a commit — which is exactly
// the question a pre-push hook asks, and the reason the ledger is a set rather than a
// slot.
func Verified(ledger, sha string) bool {
	if sha == "" {
		return false
	}
	for _, line := range lines(ledger) {
		if entry, ok := Parse(line); ok && entry.SHA == sha {
			return true
		}
	}
	return false
}

// Append returns the ledger with one commit's entry added, replacing any earlier entry
// for the same commit and keeping the newest keep entries.
//
// Replacing rather than appending blindly is what keeps one line per commit; keeping the
// newest entries bounded is what stops a shared ledger from growing without limit. The
// entries of other commits and other worktrees are preserved untouched — the writer only
// ever replaces its own commit's line.
//
// sha must be a Git object name: the line written is the line Parse reads, so anything
// else would be an entry this package's own reader refuses.
func Append(ledger, sha, scope string, at time.Time, keep int) string {
	var kept []string
	for _, line := range lines(ledger) {
		if entry, ok := Parse(line); ok && entry.SHA == sha {
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, Line(sha, scope, at))
	if keep > 0 && len(kept) > keep {
		kept = kept[len(kept)-keep:]
	}
	return strings.Join(kept, "\n") + "\n"
}

// lines returns the ledger's non-empty lines, with any carriage returns removed so a
// ledger written by another toolchain still parses.
func lines(ledger string) []string {
	var kept []string
	for _, line := range strings.Split(ledger, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return kept
}
