package lane

import (
	"strings"
	"testing"
)

// FuzzParseHolder checks the record reader the slot's decisions rest on: it never panics
// on arbitrary bytes, a record it reads always answers the two questions the slot asks
// (does it name a holder, does it carry a token), and a holder it produced renders back
// to a record that reads the same.
func FuzzParseHolder(f *testing.F) {
	f.Add("1\t2\tlabel\twho\ttoken\n")
	f.Add("")
	f.Add("\t\t\t\t")
	f.Add("1\t2\t\t\t")
	f.Add("not\ttab\tseparated at all")
	f.Add("1\tnot-a-number\tlabel\twho\ttoken")

	f.Fuzz(func(t *testing.T, record string) {
		holder := ParseHolder(record)
		if holder.Label == "" || holder.Who == "" || holder.Token == "" {
			t.Fatalf("ParseHolder(%q) = %+v, which leaves a field for a caller to interpret", record, holder)
		}
		// Whether the record names a holder is a decision, not a panic: an unknown
		// record is a claim in progress rather than an abandoned slot.
		_ = holder.Known()

		again := ParseHolder(holder.Fields())
		if again != holder {
			t.Fatalf("ParseHolder(%q) = %+v, and its own record reads as %+v", record, holder, again)
		}
		// A record that carries no token is nobody's: it is what a slot written by
		// something that does not know about per-worktree tokens looks like, and
		// treating it as this worktree's would hand out a landing twice.
		if holder.Token == NoneToken {
			if SlotStatus(&holder, holder.Who, NoneToken) != Other {
				t.Fatalf("a token-less slot is reported as this worktree's")
			}
			return
		}
		// The status and the decision must agree, or a holder would be told it holds a
		// slot its next renew is refused on. A record that names no holder is a claim
		// in progress: never this worktree's, and never taken over.
		if !holder.Known() {
			if status := SlotStatus(&holder, holder.Who, holder.Token); status != Other {
				t.Fatalf("an unreadable slot is reported as %v, want %v", status, Other)
			}
			if outcome := Decide(&holder, holder.Who, holder.Token, 0, false, holder.Since).Outcome; outcome != Unreadable {
				t.Fatalf("an unreadable slot is decided as %v, want %v", outcome, Unreadable)
			}
			return
		}
		if SlotStatus(&holder, holder.Who, holder.Token) != Mine {
			t.Fatalf("a holder is not recognised as its own with token %q", holder.Token)
		}
		if Decide(&holder, holder.Who, holder.Token, 0, false, holder.Since).Outcome != AlreadyMine {
			t.Fatalf("a holder's own acquire does not report %v", AlreadyMine)
		}
	})
}

// FuzzIdentity checks the identity two toolchains have to agree on: it never panics, it
// carries no path from either toolchain, and no part of it is dropped.
func FuzzIdentity(f *testing.F) {
	f.Add("clone", PrimaryWorktree, "main")
	f.Add("", "", "")
	f.Add("clone", "wt-386", "chore/lane/386")
	f.Add("C:/repo", `D:\repo`, "/mnt/d/repo")

	f.Fuzz(func(t *testing.T, repoID, worktree, branch string) {
		identity := Identity(repoID, worktree, branch)
		if !strings.HasPrefix(identity, repoID+"#") {
			t.Fatalf("Identity(%q, %q, %q) = %q, which drops the clone id", repoID, worktree, branch, identity)
		}
		if !strings.HasSuffix(identity, "#"+branch) {
			t.Fatalf("Identity(%q, %q, %q) = %q, which drops the branch", repoID, worktree, branch, identity)
		}
		// The parts are what a holder comparison uses, so the identity must name both
		// its worktree and its branch.
		if !strings.Contains(identity, "#primary:"+worktree+"#") {
			t.Fatalf("Identity(%q, %q, %q) = %q, which drops the worktree", repoID, worktree, branch, identity)
		}
	})
}
