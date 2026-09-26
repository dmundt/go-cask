// Package verify owns the gate run's own decisions: what a run covers, how many
// packages it may build at once, and whether an escape hatch dropped a step.
//
// It reads no environment and runs nothing. The caller supplies the variable names and
// their values, so nothing here names a repository, a path or a command — which is what
// keeps the package reusable, and what lets these three decisions be tested without a
// process.
//
// The package exists for one reason: a run that skipped a step must never be recorded as
// a verification, because the record is what authorises a push. Every comparison that
// could make a half-run look complete therefore lives here, with tests, rather than in
// the shell that held it before.
package verify

import (
	"fmt"
	"strconv"
)

// Scope is what a gate run covers.
type Scope int

const (
	// Full is the whole gate: the whole module, the race suite, the coverage loop and
	// the smoke fuzz.
	Full Scope = iota
	// Docs is the documentation gate: only the rules a documentation-only change can
	// break, which is the scope continuous integration applies to such a change.
	Docs
)

// String renders the scope the way a run's report and its ledger entry spell it.
func (s Scope) String() string {
	if s == Docs {
		return "docs"
	}
	return "full"
}

// Requested resolves the scope from the value the caller was handed and whether the
// change set is documentation-only. variable is the name that value came from, so the
// message names what the caller actually set.
//
// An empty value is the automatic scope, which lets the change set decide. An asserted
// documentation scope fails when the change is not documentation-only, so a caller that
// asked for the cheap scope can never be handed a green run for a code change; and a
// scope that is neither is refused rather than defaulted, because silently running the
// whole gate would hide the caller's typo behind minutes of work.
func Requested(value, variable string, docsOnly bool) (Scope, error) {
	switch value {
	case "", "auto":
		if docsOnly {
			return Docs, nil
		}
		return Full, nil
	case "docs":
		if !docsOnly {
			return Full, fmt.Errorf("%s=docs but the change is not documentation-only", variable)
		}
		return Docs, nil
	case "full":
		return Full, nil
	default:
		return Full, fmt.Errorf("%s must be auto, docs or full (got %q)", variable, value)
	}
}

// Jobs reads the concurrency a gate may use: a positive decimal integer, or the caller's
// fallback when the value is empty. variable is the name that value came from.
//
// The syntax is checked here rather than handed to strconv, which would accept " 8",
// "+8" and "08": a concurrency the caller did not mean is worse than the default, because
// the number reaches the number of packages built and tested at once, and a value that
// silently became 0 or 1 would turn the gate's longest step serial.
func Jobs(value, variable string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	refusal := fmt.Errorf("%s must be a positive integer (got %q)", variable, value)
	if !isPositiveDecimal(value) {
		return 0, refusal
	}
	jobs, err := strconv.Atoi(value)
	if err != nil {
		// Only a value too large for an int reaches here, and that is still the caller's
		// mistake rather than a broken tree.
		return 0, refusal
	}
	return jobs, nil
}

// isPositiveDecimal reports whether a value is a decimal integer with no sign, no
// leading zero, no surrounding space and nothing else in it.
func isPositiveDecimal(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// Escape reports whether an escape hatch dropped a step: its variable carries the value
// the gate reads as set, or the run asked for every expensive step to be dropped at once.
//
// This comparison decides whether a run may be recorded, so it is here rather than beside
// the printing. A hatch that reads as set for the report and unset for the record is
// exactly how a half-run acquires a green stamp, and the pre-push hook believes the stamp
// without asking anything else.
func Escape(value string, dropAll bool) bool {
	return dropAll || value == "true"
}
