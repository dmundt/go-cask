// Package examples selects the example programs a repository's runner executes.
//
// An example either terminates on its own, in which case the runner runs it with the
// arguments that make it finish, or it is a manual pair a reader starts by hand, in
// which case the runner names it and runs nothing. Which examples exist, what they need
// and how they are started is one repository's table; the selection rule is here.
package examples

import (
	"fmt"
	"strings"
)

// Example is one example program.
type Example struct {
	// Name is the example's directory under the repository's examples tree, and the
	// name a caller selects it by.
	Name string
	// Store is the subdirectory of the run's scratch root the example opens as its
	// store, or "" when the example opens none. A runner passes it as the store
	// argument, so a run writes nothing into the working tree.
	Store string
	// Args are the arguments that make the example complete rather than block: a
	// subcommand that terminates, or the input it needs.
	Args []string
	// Manual holds the commands that start an example a runner must not run, one per
	// line, for a reader to copy. An example with a Manual entry never runs here.
	Manual []string
}

// Runnable reports whether a runner may execute the example: one it starts by hand is
// not.
func (e Example) Runnable() bool {
	return len(e.Manual) == 0
}

// Automated returns the examples a runner executes, in table order.
func Automated(all []Example) []Example {
	var runnable []Example
	for _, example := range all {
		if example.Runnable() {
			runnable = append(runnable, example)
		}
	}
	return runnable
}

// Manual returns the examples a reader starts by hand, in table order.
func Manual(all []Example) []Example {
	var manual []Example
	for _, example := range all {
		if !example.Runnable() {
			manual = append(manual, example)
		}
	}
	return manual
}

// Select resolves the names a caller asked for to the examples to run: the named ones
// in the order given, or every automated example when no name is given. A name the
// table does not carry is an error, and so is a manual example — the error carries the
// commands to start it, because that is the only thing a runner can say about it.
func Select(names []string, all []Example) ([]Example, error) {
	if len(names) == 0 {
		return Automated(all), nil
	}
	selected := make([]Example, 0, len(names))
	for _, name := range names {
		example, found := Find(all, name)
		if !found {
			return nil, fmt.Errorf("unknown example: %s", name)
		}
		if !example.Runnable() {
			return nil, fmt.Errorf("the %s example does not terminate on its own; start it in two terminals:\n  %s",
				example.Name, strings.Join(example.Manual, "\n  "))
		}
		selected = append(selected, example)
	}
	return selected, nil
}

// Find returns the example with a name, and whether the table carries it. The table's
// first entry with that name wins, so a duplicate is a table a caller should fix rather
// than a name that resolves differently between two callers.
func Find(all []Example, name string) (Example, bool) {
	for _, example := range all {
		if example.Name == name {
			return example, true
		}
	}
	return Example{}, false
}
