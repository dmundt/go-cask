// The receipt: the record of what a green gate run covered, made portable so CI
// can reuse that run instead of repeating the suite.
//
// It owns the record's format — the version line, the fields, the order they are written
// in and the character set a check name may use — and the two derived views a verification
// needs: the identity lines that decide whether two receipts are the same evidence, and the
// canonical form of the changed path list that both sides hash.
//
// It parses and renders only. Reading a ref, signing a commit object and pushing it are the
// command's, because they are git's: this package never runs anything, and it names no
// repository.

package landing

import (
	"fmt"
	"sort"
	"strings"
)

// Version is the format's first line. A record that does not carry it is refused rather
// than read optimistically: the fields below are what a verification compares, so a reader
// that guessed at an unknown layout would be comparing nothing at all.
const Version = "cask-gate-receipt 1"

// Scope is what a run covered, as a receipt records it.
type Scope string

const (
	// Docs is the documentation gate: only the rules a documentation-only change can break.
	Docs Scope = "docs"
	// Full is the whole gate.
	Full Scope = "full"
)

// ParseScope reads a scope as a receipt writes it. It is strict on purpose: the scope is a
// token a verification compares, so a near-miss like `Docs` is a broken record rather than a
// different scope.
func ParseScope(value string) (Scope, error) {
	switch scope := Scope(value); scope {
	case Docs, Full:
		return scope, nil
	default:
		return "", fmt.Errorf("scope %q is neither docs nor full", value)
	}
}

// Field names, in the order Render writes them.
const (
	fieldCommit        = "commit"
	fieldTree          = "tree"
	fieldBase          = "base"
	fieldDiff          = "diff"
	fieldScope         = "scope"
	fieldCheck         = "check"
	fieldCoverageTiers = "coverage-tiers"
	fieldGo            = "go"
	fieldRunner        = "runner"
	fieldRun           = "run"
)

// Record is one receipt: what a green run covered, by what, and when.
type Record struct {
	// Commit is the commit the run verified, and Tree the tree it checked out.
	Commit string
	Tree   string
	// Base is the merge base the change was measured from.
	Base string
	// Diff is the hash of the changed path list the run measured. Both sides of a
	// verification recompute it, so it is the field that makes a receipt describe a change
	// rather than a commit.
	Diff string
	// Scope is what the run covered.
	Scope Scope
	// Checks are the checks the run completed, in the order it recorded them.
	Checks []string
	// CoverageTiers is how many packages the coverage gate covered, empty when the run
	// measured nothing.
	CoverageTiers string
	// Go, Runner and Run describe the run itself. They are deliberately not part of a
	// receipt's identity: two receipts for one commit that agree on everything above are
	// the same evidence, however often the gate ran and on what host.
	Go     string
	Runner string
	Run    string
}

// Parse reads a receipt. The version line must be the first line; the fields may follow in
// any order, a repeated field keeps its first value (a verification compares the first, as
// the shell helper's `head -n 1` did), and a line that names no known field is ignored —
// the format is line-oriented and extensible, so an unknown field is a later writer's and
// not this reader's business.
func Parse(payload string) (Record, error) {
	lines := strings.Split(strings.ReplaceAll(payload, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != Version {
		return Record{}, fmt.Errorf("not a %s", Version)
	}

	record := Record{}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		name, value, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		// A value carrying a CR cannot ride in the format. Parse folds a CRLF into one
		// line ending while it reads, so the CR render writes after such a value is the CR
		// of the pair the next read collapses: the value comes back shorter — or empty —
		// and the record is no longer the one the run wrote. Like a check name the
		// character set cannot carry, the line is ignored rather than carried, so every
		// value a parsed record reports is one a render is a fixed point of.
		if strings.ContainsRune(value, '\r') {
			continue
		}
		if name == fieldCheck {
			// A check line whose value cannot ride in the format is a line this reader does
			// not understand, like any other malformed one: it is ignored rather than
			// carried, so every check a parsed record reports is a name a verification can
			// match. The shell helper's `grep -qx "check <name>"` ignored such a line for
			// the same reason — it could never match a name.
			if CheckName(value) == nil {
				record.Checks = append(record.Checks, value)
			}
			continue
		}
		if seen[name] {
			continue
		}
		switch name {
		case fieldCommit:
			record.Commit = value
		case fieldTree:
			record.Tree = value
		case fieldBase:
			record.Base = value
		case fieldDiff:
			record.Diff = value
		case fieldScope:
			record.Scope = Scope(value)
		case fieldCoverageTiers:
			record.CoverageTiers = value
		case fieldGo:
			record.Go = value
		case fieldRunner:
			record.Runner = value
		case fieldRun:
			record.Run = value
		default:
			continue
		}
		seen[name] = true
	}
	return record, nil
}

// Render writes a receipt in the one order the format has: the version line, then what the
// run covered, then what it ran, then when and where. The order matters because a receipt
// is also read by a person and diffed against another run's.
func Render(record Record) string {
	var out strings.Builder
	out.WriteString(Version + "\n")
	fmt.Fprintf(&out, "%s %s\n", fieldCommit, record.Commit)
	fmt.Fprintf(&out, "%s %s\n", fieldTree, record.Tree)
	fmt.Fprintf(&out, "%s %s\n", fieldBase, record.Base)
	fmt.Fprintf(&out, "%s %s\n", fieldDiff, record.Diff)
	fmt.Fprintf(&out, "%s %s\n", fieldScope, record.Scope)
	for _, check := range record.Checks {
		fmt.Fprintf(&out, "%s %s\n", fieldCheck, check)
	}
	if record.CoverageTiers != "" {
		fmt.Fprintf(&out, "%s %s\n", fieldCoverageTiers, record.CoverageTiers)
	}
	fmt.Fprintf(&out, "%s %s\n", fieldGo, record.Go)
	fmt.Fprintf(&out, "%s %s\n", fieldRunner, record.Runner)
	fmt.Fprintf(&out, "%s %s\n", fieldRun, record.Run)
	return out.String()
}

// CheckName reports whether a check name can ride in the format. A name is one token on one
// line, so a space or a newline in it would not make the receipt wrong — it would make it
// unparseable, and a receipt that cannot be read is refused by every verification, which is
// a worse failure than a bad name.
func CheckName(name string) error {
	if name == "" {
		return fmt.Errorf("a check name is empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("check name %q has invalid characters", name)
		}
	}
	return nil
}

// Identity renders the lines that say what a receipt covers, in the format's own order and
// without the run: commit, tree, base, the diff hash, the scope, the checks and the coverage
// count. Two receipts for one commit that agree here are the same evidence, so a republish
// of the second one may be skipped — and a receipt whose *evidence* moved is the one case
// that has to replace what CI reads.
func (r Record) Identity() string {
	identity := Record{
		Commit:        r.Commit,
		Tree:          r.Tree,
		Base:          r.Base,
		Diff:          r.Diff,
		Scope:         r.Scope,
		Checks:        r.Checks,
		CoverageTiers: r.CoverageTiers,
		Go:            "",
		Runner:        "",
		Run:           "",
	}
	rendered := Render(identity)
	// Render writes the run fields even when they are empty; the identity is the lines
	// above them, which is what the caller hashes and compares.
	if at := strings.Index(rendered, fieldGo+" \n"); at >= 0 {
		return rendered[:at]
	}
	return rendered
}

// CanonicalPaths renders a changed path list in the one form both sides of a verification
// hash: one path per line, sorted, with the empty entries a diff can produce removed, and
// a trailing newline so the bytes match what `git diff --name-only | sort` produced.
//
// The hash itself is git's — the repository's object format is not this package's to
// assume, and a store may be sha256 — so the caller pipes this text to `git hash-object
// --stdin`. What this owns is that both sides feed it the same bytes.
func CanonicalPaths(paths []string) string {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	sort.Strings(kept)
	return strings.Join(kept, "\n") + "\n"
}
