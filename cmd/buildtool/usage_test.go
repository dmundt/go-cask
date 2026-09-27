package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// usageCommand matches one command line of the usage text: two spaces of indent, the
// command's name, then its description. A continuation line is indented further, so the
// word after the first two spaces is a space rather than a letter and does not match.
var usageCommand = regexp.MustCompile(`(?m)^  ([a-z][a-z-]+) +[A-Za-z]`)

// dispatchedCommand matches one arm of the dispatch switch.
var dispatchedCommand = regexp.MustCompile(`case "([^"]+)":`)

// helpSpellings are the arms that are not commands: they print the usage text itself.
var helpSpellings = map[string]bool{"-h": true, "--help": true, "help": true}

// dispatchedCommands reads the command names out of the dispatch switch. The switch is the
// authority on what runs; the usage text is the authority on what a reader is told, and the
// two are written by hand in the same file.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()

	content, err := os.ReadFile("buildtool.go")
	if err != nil {
		t.Fatalf("read buildtool.go: %v", err)
	}
	source := string(content)
	start := strings.Index(source, "func run(args []string")
	if start < 0 {
		t.Fatal("buildtool.go no longer declares run(args []string, out, errOut io.Writer)")
	}
	// The function ends at the next top-level declaration, which is the whole switch.
	body := source[start+1:]
	if end := strings.Index(body, "\nfunc "); end >= 0 {
		body = body[:end]
	}

	names := map[string]bool{}
	for _, match := range dispatchedCommand.FindAllStringSubmatch(body, -1) {
		names[match[1]] = true
	}
	if len(names) == 0 {
		t.Fatal("found no commands in the dispatch switch")
	}
	return names
}

// TestUsageListsEveryCommand pins the help text against the dispatch switch: a command that
// runs but is undocumented is invisible to the person who needs it, and a documented command
// that does not run is a promise the tool breaks. Both are one hand-edit away, and the
// command list is long enough that neither is noticed in review.
func TestUsageListsEveryCommand(t *testing.T) {
	t.Parallel()

	documented := map[string]bool{}
	for _, match := range usageCommand.FindAllStringSubmatch(usage, -1) {
		documented[match[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("found no commands in the usage text")
	}

	dispatched := dispatchedCommands(t)
	for name := range dispatched {
		if helpSpellings[name] {
			continue
		}
		if !documented[name] {
			t.Errorf("the command %q runs but is not in the usage text", name)
		}
	}
	for name := range documented {
		if !dispatched[name] {
			t.Errorf("the usage text documents %q, which nothing dispatches", name)
		}
	}
}
