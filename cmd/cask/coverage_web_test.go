package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestRunWebUsesTheDefaultStore pins the documented default the viewer shape
// applies when the global -store is not given: `./objects` beside the working
// directory (cli.md §1, §2). The run is driven with -show-token so the notice
// carries the observable proof that a real viewer started — a login deep link —
// and the working directory is a temp directory, so the default's new store
// directory is cleaned up with the rest of the test's state.
func TestRunWebUsesTheDefaultStore(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(viewerTokenEnv, "")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	var code int
	stdout, _ := captureStreams(t, func() {
		code = runWeb(ctx, modeFlags{}, []string{"-bind", "127.0.0.1:0", "-no-open", "-show-token"})
	})
	cancel()

	if code != 0 {
		t.Fatalf("runWeb without -store exit = %d, want 0 (stdout %q)", code, stdout)
	}
	if !strings.Contains(stdout, "cask web: log in once at http://127.0.0.1:") {
		t.Fatalf("stdout = %q, want the login notice for a started viewer", stdout)
	}
	if _, err := os.Stat("objects"); err != nil {
		t.Fatalf("the default store directory ./objects was not opened: %v", err)
	}
}

// TestRunWebRefusesANonLoopbackBindWithoutTheOverride pins viewer-security §4 at
// the CLI boundary: a bind reachable from the network is refused with a logged
// reason (exit 1) before anything is opened or announced, so the refusal is not
// a silent fallback to a narrower bind.
func TestRunWebRefusesANonLoopbackBindWithoutTheOverride(t *testing.T) {
	logs := installRecorder(t)
	var code int
	stdout, stderr := captureStreams(t, func() {
		code = runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-bind", "0.0.0.0:0", "-no-open"})
	})
	if code != 1 {
		t.Fatalf("runWeb -bind 0.0.0.0:0 exit = %d, want 1 (refused)", code)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("refusal wrote to the streams (stdout %q, stderr %q), want the log only", stdout, stderr)
	}
	if !strings.Contains(logs.String(), "refusing to bind the viewer to a non-loopback address without HTTPS") {
		t.Fatalf("log = %q, want the refusal naming the missing override", logs.String())
	}
}

// TestRunWebRejectsAnUnknownHashAlgorithm pins the viewer's algorithm
// validation: an unknown -hash-algo name is a usage error (exit 2) that names
// the value, and it is refused before the preview graph is rebuilt with it
// (cli.md §4).
func TestRunWebRejectsAnUnknownHashAlgorithm(t *testing.T) {
	logs := installRecorder(t)
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()},
		[]string{"-hash-algo", "sha1", "-bind", "127.0.0.1:0", "-no-open"}); code != 2 {
		t.Fatalf("runWeb -hash-algo sha1 exit = %d, want 2 (usage)", code)
	}
	logged := logs.String()
	if !strings.Contains(logged, "invalid viewer hash algorithm") || !strings.Contains(logged, "sha1") {
		t.Fatalf("log = %q, want it to name the invalid algorithm", logged)
	}
}

// TestWebUsageRendersTheTokenDisplayFlag pins the tri-state flag's help text:
// -show-token defaults to "auto" (the flag was never set) and reports the
// explicit boolean once it is, so the usage output cannot advertise a default
// the run does not apply (cli.md §2, §4).
func TestWebUsageRendersTheTokenDisplayFlag(t *testing.T) {
	var a webArgs
	flags := webFlags(&a, "./objects", "")
	if got := a.showToken.String(); got != "auto" {
		t.Fatalf("unset -show-token renders as %q, want %q", got, "auto")
	}
	if err := flags.Parse([]string{"-show-token=false"}); err != nil {
		t.Fatal(err)
	}
	if got := a.showToken.String(); got != "false" {
		t.Fatalf("-show-token=false renders as %q, want %q", got, "false")
	}
	if err := flags.Parse([]string{"-show-token"}); err != nil {
		t.Fatal(err)
	}
	if got := a.showToken.String(); got != "true" {
		t.Fatalf("bare -show-token renders as %q, want %q", got, "true")
	}
	if !strings.Contains(commandUsage(webFlags(new(webArgs), "./objects", "")), "-show-token") {
		t.Fatal("web usage does not document -show-token")
	}
}

// TestRunWebIgnoresRoleTokenEntriesThatGrantNothing pins the -tokens grammar at
// startup: an entry without a "=" or with an empty token value is dropped rather
// than becoming a role granted to the empty string, and the run still starts
// (cli.md §2, viewer-security §5).
func TestRunWebIgnoresRoleTokenEntriesThatGrantNothing(t *testing.T) {
	logs := installRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	var code int
	stdout, _ := captureStreams(t, func() {
		code = runWeb(ctx, modeFlags{store: t.TempDir()},
			[]string{"-bind", "127.0.0.1:0", "-no-open", "-show-token", "-tokens", "admin=tok,novalue,empty="})
	})
	cancel()
	if code != 0 {
		t.Fatalf("runWeb with droppable -tokens entries exit = %d, want 0 (log %q)", code, logs.String())
	}
	if !strings.Contains(stdout, "/viewer/?token=") {
		t.Fatalf("stdout = %q, want the viewer to have started", stdout)
	}
}

// TestRunWebReportsAnUnusableTokenFile pins the startup token's failure path
// (cli.md §4, viewer-security §11): a -token-file that cannot be read fails
// startup (exit 1) with a logged reason naming the flag, and the failure is
// recorded — never a token, because none was obtained — before any server is
// opened.
func TestRunWebReportsAnUnusableTokenFile(t *testing.T) {
	logs := installRecorder(t)
	missing := filepath.Join(t.TempDir(), "absent-token")
	code := runWeb(context.Background(), modeFlags{store: t.TempDir()},
		[]string{"-token-file", missing, "-bind", "127.0.0.1:0", "-no-open"})
	if code != 1 {
		t.Fatalf("runWeb -token-file %s exit = %d, want 1", missing, code)
	}
	logged := logs.String()
	if !strings.Contains(logged, "viewer startup token") || !strings.Contains(logged, missing) {
		t.Fatalf("log = %q, want it to name the unreadable token file", logged)
	}
	if strings.Contains(logged, "?token=") {
		t.Fatalf("log = %q, must never carry a login link", logged)
	}
}

// TestRunWebServesASeededPreviewStore drives the startup path with the
// deterministic preview graph present: the viewer takes the graph as its
// reference and reachability source (cli.md §2) and the run still starts and
// shuts down cleanly, still announcing the login link only to stdout.
func TestRunWebServesASeededPreviewStore(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "seed-preview", "-count", "8"); code != 0 {
		t.Fatalf("seed-preview exit = %d, want 0", code)
	}
	stdout, stderr, logged := runWebNoticeOn(t, "127.0.0.1:0", "-show-token", "-store", mf.store)
	if !strings.Contains(stdout, "cask web: log in once at http://127.0.0.1:") {
		t.Fatalf("stdout = %q, want the login notice for a preview store", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want the notice on stdout only", stderr)
	}
	if strings.Contains(logged, "?token=") {
		t.Fatalf("the process log contains the login link:\n%s", logged)
	}
}

// TestRunWebOpensTheBrowserOnALoginBind pins the browser-launch decision at the
// CLI boundary (cli.md §2, viewer-security §11): with a loopback bind, a
// displayed token and no -no-open, the viewer hands the token deep link to the
// platform's browser helper — and a helper that cannot be started is not fatal.
// The helper is a directory with no PATH entry on it, so the launch is exercised
// deterministically instead of opening a real browser on the test machine.
func TestRunWebOpensTheBrowserOnALoginBind(t *testing.T) {
	t.Setenv(viewerTokenEnv, "")
	t.Setenv("PATH", t.TempDir()) // no browser helper can be found here
	logs := installRecorder(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	var code int
	stdout, _ := captureStreams(t, func() {
		code = runWeb(ctx, modeFlags{store: t.TempDir()},
			[]string{"-bind", "127.0.0.1:0", "-show-token"})
	})
	cancel()

	if code != 0 {
		t.Fatalf("runWeb launching a missing browser exit = %d, want 0 (a browser is best-effort)", code)
	}
	if !strings.Contains(stdout, "/viewer/?token=") {
		t.Fatalf("stdout = %q, want the login deep link a loopback bind may display", stdout)
	}
	if !strings.Contains(logs.String(), "open browser") {
		t.Fatalf("log = %q, want the best-effort browser failure recorded", logs.String())
	}
}

// Uncovered in web.go, with the reason each branch has no deterministic test
// (testing-strategy §5):
//
//   - the serve goroutine's error report (web.go:272-274), runWeb's
//     post-signal exit code (web.go:300), and the shutdown/force-close reports
//     (web.go:305-309) need Serve or Shutdown to fail on the listener this same
//     run just opened. Nothing the CLI accepts produces that, and the graceful
//     path those branches guard is the one every runWeb test here exercises.
//   - openBrowser's successful-child path (web.go:491-494) would need a browser
//     helper that really starts and then really exits non-zero; the failure
//     branch above it is covered by a PATH with no helper on it, and a helper
//     that starts is not the viewer's behaviour.
//   - randomToken's rand.Read failure (web.go:505-506) and stdoutIsTerminal's
//     Stat failure (web.go:392-393) originate in the OS and cannot be provoked
//     by any input the CLI accepts.
//   - isLoopbackBind's "localhost" fallback and browserCommand's per-platform
//     mapping are not viewer branches: both are pinned directly by
//     main_test.go.

// TestRunWebReportsAnUnreadableStore pins the preview walk's failure at the CLI
// boundary (cli.md §2): a store whose objects cannot be probed fails the viewer
// startup (exit 1) with a logged reason, instead of starting a viewer whose
// reference graph would be silently wrong. The probe is made to fail — rather
// than answer "not stored", the ordinary-store case that must NOT fail startup
// — by putting a regular file where the fan-out layout needs a directory: the
// first preview object's own fan-out bucket, so the very first existence check
// reports "not a directory". A store that merely lacks the preview graph stays
// the success case (TestRunWebNoticeGoesToStdout).
func TestRunWebReportsAnUnreadableStore(t *testing.T) {
	storeDir := t.TempDir()
	first, err := previewObjectFor(0, nil, sha256.New())
	if err != nil {
		t.Fatal(err)
	}
	// The probe addresses <store>/<first two hex chars>/<full hex>, so a
	// regular file at <store>/<first two hex chars> breaks the stat behind
	// Exists for the first object the walk probes.
	bucket := first.digest.String()[:2]
	if err := os.WriteFile(filepath.Join(storeDir, bucket), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	logs := installRecorder(t)
	// The probe reports an unaddressable fan-out bucket as an error only where
	// the OS does: on Windows a regular file used as a directory reports
	// ERROR_PATH_NOT_FOUND, which Go maps to fs.ErrNotExist, so the probe
	// answers "not stored" and the viewer starts normally. The branch is
	// unreachable there, not untested.
	if runtime.GOOS == "windows" {
		t.Skip("a file where a directory belongs maps to fs.ErrNotExist on Windows, so the probe does not fail")
	}
	// Bounded so a regression here fails the test instead of hanging the
	// package until its timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := runWeb(ctx, modeFlags{store: storeDir},
		[]string{"-bind", "127.0.0.1:0", "-no-open"}); code != 1 {
		t.Fatalf("runWeb over an unprobeable store exit = %d, want 1", code)
	}
	logged := logs.String()
	if !strings.Contains(logged, "build preview references") || !strings.Contains(logged, "not a directory") {
		t.Fatalf("log = %q, want it to name the failed preview build and its cause", logged)
	}
}
