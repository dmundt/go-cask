package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// synopsisFlag finds the -flag tokens a command synopsis documents. A flag
// stands at the start of the synopsis, after a space, or inside the [-flag ...]
// brackets and <operand|--flag> alternatives cli.md §2 uses; the '-' of an
// operand such as "<file|->" does not introduce a flag.
var synopsisFlag = regexp.MustCompile(`(?:^|[\s\[|])-{1,2}([a-zA-Z][a-zA-Z0-9-]*)`)

// TestUsageDocumentsEveryFlag pins the single-source contract of the command
// table: every flag a command's parser accepts appears in its documented
// synopsis, and the synopsis documents nothing the parser rejects (cli.md §2,
// §4). This is the drift that left -hash-algo and the -json flags undocumented
// before.
func TestUsageDocumentsEveryFlag(t *testing.T) {
	for _, c := range commands {
		documented := map[string]bool{}
		for _, m := range synopsisFlag.FindAllStringSubmatch(c.synopsis(), -1) {
			documented[m[1]] = true
		}
		if c.flags == nil {
			if len(documented) != 0 {
				t.Errorf("%s documents %v but accepts no flags", c.synopsis(), documented)
			}
			continue
		}
		c.flags().VisitAll(func(f *flag.Flag) {
			if !documented[f.Name] {
				t.Errorf("%s accepts -%s but does not document it", c.name, f.Name)
			}
			delete(documented, f.Name)
		})
		for name := range documented {
			t.Errorf("%s documents -%s but does not accept it", c.name, name)
		}
	}
}

// TestUsageListsEveryCommand: the top-level help and the per-command help are
// generated from the command table.
func TestUsageListsEveryCommand(t *testing.T) {
	text := usage()
	for _, c := range commands {
		if !strings.Contains(text, c.synopsis()) {
			t.Errorf("usage does not describe %q", c.synopsis())
		}
	}
	if webUsage := commandUsage(webFlags(new(webArgs), "")); !strings.Contains(webUsage, "-hash-algo") {
		t.Errorf("web usage does not document -hash-algo:\n%s", webUsage)
	}
}

// TestPutGetFlagOrder covers the operand-then-flag order cli.md §2 specifies for
// put ("put <file> [-json]") and get ("get <hash> [-o <file>]"), including the
// -flag=value form, and the unknown-flag rejection the hand-rolled loops lacked.
func TestPutGetFlagOrder(t *testing.T) {
	file := writeTemp(t, "flag order")

	mf := localMF(t)
	out, code := run(t, mf, "put", file, "-json")
	if code != 0 {
		t.Fatalf("put <file> -json exit %d", code)
	}
	var got struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("put <file> -json output %q: %v", out, err)
	}
	if got.Hash == "" {
		t.Fatalf("put <file> -json output %q has no hash", out)
	}

	// The flag may also precede the operand and take the =value form.
	if _, code := run(t, localMF(t), "put", "-json", file); code != 0 {
		t.Fatalf("put -json <file> exit %d", code)
	}
	if _, code := run(t, mf, "put", file, "-json=true"); code != 0 {
		t.Fatalf("put <file> -json=true exit %d", code)
	}
	if _, code := run(t, mf, "put", file, "--json"); code != 0 {
		t.Fatalf("put <file> --json exit %d", code)
	}

	// get accepts -o and -o=<file> after the hash.
	for _, args := range [][]string{
		{"get", got.Hash, "-o", filepath.Join(t.TempDir(), "a.bin")},
		{"get", got.Hash, "-o=" + filepath.Join(t.TempDir(), "b.bin")},
	} {
		if _, code := run(t, mf, args[0], args[1:]...); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
	}

	for _, args := range [][]string{
		{"put", "-nope", file},
		{"get", got.Hash, "-nope"},
		{"get", got.Hash, "-o"}, // -o needs a value
	} {
		if _, code := run(t, mf, args[0], args[1:]...); code != 2 {
			t.Errorf("%v exit %d, want 2 (usage)", args, code)
		}
	}
}

// TestParseGlobalFlagForms covers the global flag forms cli.md §4 allows
// (-store <path>, -store=<path>, --store=<path>) and the usage errors for an
// unknown flag and a missing command.
func TestParseGlobalFlagForms(t *testing.T) {
	for _, args := range [][]string{
		{"-store", "/tmp/repo", "list"},
		{"-store=/tmp/repo", "list"},
		{"--store=/tmp/repo", "list"},
		{"--store", "/tmp/repo", "list"},
	} {
		mf, cmd, _, err := parseGlobal(args)
		if err != nil || cmd != "list" || mf.store != "/tmp/repo" {
			t.Fatalf("parseGlobal(%v) = (%+v, %q, %v), want store /tmp/repo and command list", args, mf, cmd, err)
		}
	}
	for _, args := range [][]string{
		{"-nope", "list"}, // unknown flag
		{"-store"},        // missing value
		{},                // no command
	} {
		if _, _, _, err := parseGlobal(args); err == nil {
			t.Fatalf("parseGlobal(%v) error = nil, want a usage error", args)
		}
	}
	if _, _, _, err := parseGlobal([]string{"-help"}); !errors.Is(err, errHelp) {
		t.Fatalf("parseGlobal(-help) error = %v, want errHelp", err)
	}
}

// TestStoreOpenFailureIsRuntimeError: only a missing -store is a usage error; a
// failure of the filesystem backend is a runtime error, exit 1 (cli.md §3).
func TestStoreOpenFailureIsRuntimeError(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, modeFlags{store: notADir}, "list"); code != 1 {
		t.Fatalf("list on an unopenable store: exit %d, want 1 (runtime)", code)
	}
}

// TestHelpRequestsExitZero: -h/-help prints the command's usage and succeeds
// instead of failing as a usage error — including when -store is missing, so
// the help text is always reachable (cli.md §3).
func TestHelpRequestsExitZero(t *testing.T) {
	mf := localMF(t)
	for _, tc := range []struct {
		mf   modeFlags
		cmd  string
		want string
	}{
		{mf, "list", "usage: cask list"},
		{mf, "stats", "usage: cask stats"},
		{modeFlags{}, "list", "usage: cask list"}, // no -store yet
		{mf, "put", "usage: cask put"},            // flags may follow the operand
		{mf, "version", "usage: cask version"},
	} {
		out, code := run(t, tc.mf, tc.cmd, "-h")
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%s -h = (%q, %d), want %q and exit 0", tc.cmd, out, code, tc.want)
		}
	}
	var webCode int
	webOut := captureStdout(t, func() { webCode = runWeb(context.Background(), mf, []string{"-help"}) })
	if webCode != 0 || !strings.Contains(webOut, "usage: cask web") {
		t.Fatalf("web -help = (%q, %d), want the web usage and exit 0", webOut, webCode)
	}
}
