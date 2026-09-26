package gate

import (
	"testing"
	"time"
)

// FuzzParse checks the reader the pre-push hook depends on: it never panics on arbitrary
// input, an entry it accepts names a hex commit and a scope, and what it accepts it
// renders back to a line that parses to the same entry.
func FuzzParse(f *testing.F) {
	f.Add("aaa111122223333444455556666777788889999 full 2026-09-25T14:46:49Z")
	f.Add("")
	f.Add("\t\t\t")
	f.Add("not a commit at all")
	f.Add("aaa111122223333444455556666777788889999")
	f.Add("aaa111122223333444455556666777788889999 docs")

	f.Fuzz(func(t *testing.T, line string) {
		entry, ok := Parse(line)
		if !ok {
			return
		}
		if !isObjectName(entry.SHA) {
			t.Fatalf("Parse accepted %q as a commit", entry.SHA)
		}
		if entry.Scope == "" {
			t.Fatalf("Parse accepted %q with no scope", line)
		}
		// A rendered entry must read back as the same commit and scope, so the writer
		// and the reader cannot disagree.
		again, ok := Parse(Line(entry.SHA, entry.Scope, entry.At))
		if !ok || again.SHA != entry.SHA || again.Scope != entry.Scope {
			t.Fatalf("round trip of %q produced %+v, %v", line, again, ok)
		}
	})
}

// FuzzVerifiedAndAppend checks the two operations a gate performs on the ledger:
// neither panics, and the writer never loses the commit it just wrote while keeping one
// line per commit.
func FuzzVerifiedAndAppend(f *testing.F) {
	f.Add("aaa111122223333444455556666777788889999 full 2026-09-25T14:46:49Z\n",
		"bbb111122223333444455556666777788889999")
	f.Add("", "")
	f.Add("garbage\n\nmore garbage\n", "ccc111122223333444455556666777788889999")

	f.Fuzz(func(t *testing.T, ledger, sha string) {
		at := time.Unix(0, 0).UTC()
		updated := Append(ledger, sha, "full", at, 200)

		// A ledger short enough to fit the bound must keep every other commit's entry:
		// the writer only ever replaces its own line.
		if len(lines(ledger)) < 199 {
			for _, line := range lines(ledger) {
				entry, ok := Parse(line)
				if !ok || entry.SHA == sha {
					continue
				}
				if !Verified(updated, entry.SHA) {
					t.Fatalf("Append dropped another commit's entry %q", line)
				}
			}
		}
		// The entry just written is the newest, so the bound never drops it. The writer
		// renders whatever commit it is handed and its reader accepts an object name,
		// which is what a caller obtains from `git rev-parse HEAD`.
		if isObjectName(sha) && !Verified(updated, sha) {
			t.Fatalf("Append wrote an entry that does not verify for %q", sha)
		}
		if len(lines(updated)) > 200 {
			t.Fatalf("Append kept %d lines, want at most 200", len(lines(updated)))
		}
	})
}
