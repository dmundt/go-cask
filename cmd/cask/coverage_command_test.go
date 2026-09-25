package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Uncovered outside web.go, with the reason each branch has no deterministic
// test (testing-strategy §5):
//
//   - main() itself (main.go:206-216) is the process entry point: it reads
//     os.Args and calls os.Exit, so a test can only exercise it by replacing the
//     test binary. Everything it does is covered through its parts — parseGlobal
//     (TestParseGlobalFlagForms), usage (TestUsageListsEveryCommand), the
//     errHelp branch (TestParseGlobalFlagForms), runOp
//     (TestCommandLookupAndUnknownCommand) and the exit-code mapping
//     (TestExitCodes) each have their own test. The errHelp test in main() is
//     the same reason: parseGlobal is the only place that produces errHelp, and
//     it is asserted there.
//   - helpRequest's nil-parser guard (flags.go:66-67) is a defensive branch no
//     caller can enter: reportError only constructs a helpRequested from a
//     parser built by the command table's flags function, and the table's only
//     entry without one (version) has versionFlags. runStoreOp consults it with
//     spec.flags, which is nil only for the command whose run function never
//     reaches it.
//   - acquireStoreLock's two failure branches (lock.go:47 and 51-53) are covered
//     through the one the CLI can produce: an unwritable store directory fails
//     OpenFile and is reported as "lock store: <reason>"
//     (TestLockAcquireFailureIsReported, which skips itself where directory
//     permissions are not enforced). Its sibling at 51-53 needs f.Close to fail
//     on a file the function just created and wrote two lines into, which the OS
//     does not report; both print the same message, and the cleanup it guards
//     (removing the lock file) is exercised in the release path by every
//     maintenance test.

// TestHelpRequestedError pins the sentinel's message: main and reportError
// distinguish a -h/-help request by type, and the text is what the fallback
// path prints when the request reaches reportError instead of main.
func TestHelpRequestedError(t *testing.T) {
	if got := (helpRequested{}).Error(); got != "help requested" {
		t.Fatalf("helpRequested.Error() = %q, want %q", got, "help requested")
	}
}

// TestFlagsFirstReordersAndHonorsTheMarker pins flagsFirst's contract directly,
// because it is the seam that makes "put <file> [-json]" work: a flag and its
// separate value move in front of the operands, and `--` ends the flag region —
// everything after it is kept as an operand, in order, without further
// reordering.
//
// The CLI-level consequence of the marker is recorded here as a limitation
// rather than asserted as behaviour: `put -- -json` still exits 2, because
// flagsFirst drops the `--` before handing the reordered slice to the flag
// package, which then consumes the `-json` operand as its own flag. Storing a
// file whose name IS a defined flag is therefore not reachable through the
// marker; a file whose name is merely dashed (`-data.bin`) fails the same way
// one step earlier, in flagsFirst's unknown-flag check.
func TestFlagsFirstReordersAndHonorsTheMarker(t *testing.T) {
	t.Run("operand then flag", func(t *testing.T) {
		got, err := flagsFirst(putFlags(new(putArgs)), []string{"data.bin", "-json"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, []string{"-json", "data.bin"}) {
			t.Fatalf("flagsFirst = %v, want the flag moved in front of the operand", got)
		}
	})

	t.Run("flag with a separate value", func(t *testing.T) {
		got, err := flagsFirst(getFlags(new(getArgs)), []string{"data.bin", "-o", "out.bin"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, []string{"-o", "out.bin", "data.bin"}) {
			t.Fatalf("flagsFirst = %v, want the flag and its value moved in front of the operand", got)
		}
	})

	t.Run("end of flags", func(t *testing.T) {
		got, err := flagsFirst(putFlags(new(putArgs)), []string{"--", "--json"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, []string{"--json"}) {
			t.Fatalf("flagsFirst(-- --json) = %v, want the marker's remainder kept as the operand list", got)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		if _, err := flagsFirst(putFlags(new(putArgs)), []string{"-nope"}); err == nil {
			t.Fatal("flagsFirst accepted a flag its parser does not define")
		}
	})
}

// TestCommandLookupAndUnknownCommand pins the dispatch table's contract
// (cli.md §2): every documented command resolves, an unknown word does not, and
// running one prints the usage and is a usage error (exit 2) rather than a
// panic or a silent success.
func TestCommandLookupAndUnknownCommand(t *testing.T) {
	for _, c := range commands {
		if got, ok := command(c.name); !ok || got.name != c.name {
			t.Fatalf("command(%q) = (%+v, %v), want the table entry", c.name, got, ok)
		}
		if c.flags == nil && c.op == nil && c.run == nil {
			t.Fatalf("command %q has neither an op, a run, nor flags", c.name)
		}
	}
	if _, ok := command("nonexistent"); ok {
		t.Fatal("command(nonexistent) resolved, want a miss")
	}

	out, stderr, code := runBoth(t, modeFlags{}, "nonexistent")
	if code != 2 {
		t.Fatalf("unknown command exit = %d, want 2 (usage)", code)
	}
	if out != "" {
		t.Fatalf("unknown command stdout = %q, want nothing", out)
	}
	if !strings.Contains(stderr, `unknown command "nonexistent"`) || !strings.Contains(stderr, "usage: cask") {
		t.Fatalf("unknown command stderr = %q, want the unknown-command line and the usage text", stderr)
	}
}

// TestCommandUsageFallsBackForAnUnknownParser pins commandUsage's second shape:
// a parser whose name is not in the command table falls back to the generic
// "usage: cask <name> [flags]" line instead of rendering no usage at all. The
// fallback is what keeps a reported error printable when a parser and the table
// ever disagree about a command's name.
func TestCommandUsageFallsBackForAnUnknownParser(t *testing.T) {
	flags := newFlagSet("not-a-command")
	flags.Bool("verbose", false, "a flag the fallback still documents")

	out := commandUsage(flags)
	if !strings.Contains(out, "usage: cask not-a-command [flags]") {
		t.Fatalf("commandUsage = %q, want the generic synopsis for an unregistered parser", out)
	}
	if !strings.Contains(out, "-verbose") {
		t.Fatalf("commandUsage = %q, want the parser's own flags documented", out)
	}
}

// TestLockAcquireFailureIsReported pins the lock's non-contention failure
// (lock.go): a store directory the process cannot write to is reported as
// "lock store" with the filesystem's own reason, not as another process holding
// the lock — the operator must be able to tell a permissions problem from a
// live sweep.
//
// Directory permissions are not enforced in every sandbox (a root-owned
// container ignores them), so the test first proves the restriction is real and
// skips rather than passing on a lock it never failed to take.
func TestLockAcquireFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced in this environment")
	}

	_, err := acquireStoreLock(dir)
	if err == nil {
		t.Fatal("acquireStoreLock in an unwritable directory = nil, want an error")
	}
	if !strings.Contains(err.Error(), "lock store") {
		t.Fatalf("error = %q, want it to name the failed lock", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error = %q, want it to wrap the filesystem's permission error", err)
	}
}

// TestLockHolderWithoutAPIDIsUnknown pins the two ways a lock file fails to name
// its holder — an unreadable record and a line that is not "pid=<number>" —
// both of which report "an unknown process" so a stale lock is still actionable
// (lock.go, cli.md §2).
func TestLockHolderWithoutAPIDIsUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)
	if err := os.WriteFile(path, []byte("held by someone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLockHolder(path); got != "an unknown process" {
		t.Fatalf("readLockHolder(unparseable) = %q, want %q", got, "an unknown process")
	}
	if got := readLockHolder(filepath.Join(dir, "absent.lock")); got != "an unknown process" {
		t.Fatalf("readLockHolder(absent) = %q, want %q", got, "an unknown process")
	}

	// The contention message names the parsed holder when there is one.
	if err := os.WriteFile(path, []byte("pid=4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireStoreLock(dir); err == nil {
		t.Fatal("acquireStoreLock over an existing lock file = nil, want a contention error")
	} else if !strings.Contains(err.Error(), "process 4242") {
		t.Fatalf("contention error = %q, want it to name the recorded holder", err)
	}
}
