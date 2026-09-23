package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHandler captures every record the code under test logs, whatever its
// level: the contract for the viewer's startup token is that NO log level emits
// it, so a guard has to listen at the lowest level (viewer-security §9, §11).
// It is safe for concurrent use because runWeb logs from its serving goroutine.
type recordingHandler struct {
	mu   sync.Mutex
	text strings.Builder
}

// Enabled implements slog.Handler at every level.
func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler, recording the level, message, and attributes.
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text.WriteString(r.Level.String())
	h.text.WriteString(" ")
	h.text.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		h.text.WriteString(" ")
		h.text.WriteString(a.Key)
		h.text.WriteString("=")
		h.text.WriteString(a.Value.String())
		return true
	})
	h.text.WriteString("\n")
	return nil
}

// WithAttrs implements slog.Handler.
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// String returns everything recorded so far.
func (h *recordingHandler) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

// installRecorder makes a recording handler the process default logger for the
// duration of the test. The default logger is process-wide, so the previous one
// is restored by cleanup; the cmd/cask tests run sequentially, so no other test
// observes the swap.
func installRecorder(t *testing.T) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestAnnounceLoginNeverLogsToken is the regression guard for issue #179: the
// startup token may reach the operator's terminal, but no slog handler may ever
// see it, at any level — and neither may it see the login link that carries the
// token. It fails if any log call in the announcement carries either — the code
// before the #179 fix did exactly that with
// slog.Warn("viewer startup token", "admin_token", token).
func TestAnnounceLoginNeverLogsToken(t *testing.T) {
	const token = "AAAA-BBBB-CCCC"
	baseURL := "http://127.0.0.1:8080"
	for _, tc := range []struct {
		name      string
		generated bool
		display   displayChoice
		wantShown bool
	}{
		{"generated, display chosen", true, displayShown, true},
		{"generated, hidden by default", true, displayHidden, false},
		{"generated, suppressed by the operator", true, displaySuppressed, false},
		{"supplied, display chosen", false, displayShown, false},
		{"supplied, hidden", false, displayHidden, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := installRecorder(t)
			var out strings.Builder
			announceLogin(&out, loginNotice{
				bind:      "127.0.0.1:8080",
				baseURL:   baseURL,
				token:     token,
				generated: tc.generated,
				display:   tc.display,
			})
			logged := logs.String()
			if strings.Contains(logged, token) {
				t.Fatalf("the process log contains the startup token:\n%s", logged)
			}
			if strings.Contains(logged, loginURL(baseURL, token)) {
				t.Fatalf("the process log contains the login link:\n%s", logged)
			}
			if shown := strings.Contains(out.String(), token); shown != tc.wantShown {
				t.Fatalf("token shown = %v, want %v (output %q)", shown, tc.wantShown, out.String())
			}
		})
	}
}

// TestTokenDisplayResolve pins the -show-token tri-state: an absent flag keeps
// the terminal heuristic — the behavior before the flag existed — an explicit
// true forces the one-time hint in a run without a terminal, and an explicit
// false suppresses it even on one (cli.md §2).
func TestTokenDisplayResolve(t *testing.T) {
	for _, tc := range []struct {
		name        string
		display     tokenDisplay
		interactive bool
		want        displayChoice
	}{
		{"unset on a terminal", tokenDisplay{}, true, displayShown},
		{"unset without a terminal", tokenDisplay{}, false, displayHidden},
		{"forced without a terminal", tokenDisplay{set: true, value: true}, false, displayShown},
		{"forced on a terminal", tokenDisplay{set: true, value: true}, true, displayShown},
		{"suppressed on a terminal", tokenDisplay{set: true, value: false}, true, displaySuppressed},
		{"suppressed without a terminal", tokenDisplay{set: true, value: false}, false, displaySuppressed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.display.resolve(tc.interactive); got != tc.want {
				t.Fatalf("resolve(%v) = %v, want %v", tc.interactive, got, tc.want)
			}
		})
	}
}

// TestShowTokenFlagForms pins the flag grammar: the bare form means true, both
// =-forms are accepted, and any other value is a usage error rather than a
// silently ignored flag (cli.md §2, §4).
func TestShowTokenFlagForms(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		want  tokenDisplay
		usage bool
	}{
		{args: nil, want: tokenDisplay{}},
		{args: []string{"-show-token"}, want: tokenDisplay{set: true, value: true}},
		{args: []string{"-show-token=true"}, want: tokenDisplay{set: true, value: true}},
		{args: []string{"-show-token=false"}, want: tokenDisplay{set: true, value: false}},
		{args: []string{"--show-token=false"}, want: tokenDisplay{set: true, value: false}},
		{args: []string{"-show-token=maybe"}, usage: true},
	} {
		var a webArgs
		err := webFlags(&a, "", "").Parse(tc.args)
		if tc.usage {
			if err == nil {
				t.Errorf("web -show-token=maybe accepted, want a usage error")
			}
			continue
		}
		if err != nil {
			t.Fatalf("web %v: %v", tc.args, err)
		}
		if a.showToken != tc.want {
			t.Errorf("web %v parsed -show-token as %+v, want %+v", tc.args, a.showToken, tc.want)
		}
	}
}

// TestNoticeOrigin pins which binds may receive a printed login link: only a
// loopback bind, whose plain http:// origin can hold the always-Secure session
// cookie (viewer-security §7).
func TestNoticeOrigin(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want string
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"[::1]:8080", "http://[::1]:8080"},
		{"localhost:8080", "http://localhost:8080"},
		{"0.0.0.0:8080", ""},
		{"192.0.2.10:8080", ""},
		{":8080", ""},
	} {
		if got := noticeOrigin(tc.addr); got != tc.want {
			t.Errorf("noticeOrigin(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestAnnounceLoginNonLoopbackPrintsNoLink is the non-loopback half of the
// printing rule: a bind whose plain http:// origin cannot hold a session gets no
// login link at all, and the notice names the bind and the https:// expectation
// instead (cli.md §2, viewer-security §7).
func TestAnnounceLoginNonLoopbackPrintsNoLink(t *testing.T) {
	const (
		bind  = "0.0.0.0:8080"
		token = "AAAA-BBBB-CCCC"
	)
	for _, tc := range []struct {
		name      string
		generated bool
		display   displayChoice
		wantToken bool
	}{
		{"generated, display chosen", true, displayShown, true},
		{"generated, hidden", true, displayHidden, false},
		{"supplied, display chosen", false, displayShown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := installRecorder(t)
			var out strings.Builder
			announceLogin(&out, loginNotice{
				bind:      bind,
				baseURL:   noticeOrigin(bind),
				token:     token,
				generated: tc.generated,
				display:   tc.display,
			})
			got := out.String()
			if strings.Contains(got, "/viewer/") || strings.Contains(got, "?token=") || strings.Contains(got, "http://"+bind) {
				t.Errorf("non-loopback notice printed a login link: %q", got)
			}
			if !strings.Contains(got, bind) {
				t.Errorf("non-loopback notice does not name the bind %q: %q", bind, got)
			}
			if !strings.Contains(got, "https://") {
				t.Errorf("non-loopback notice does not state the https:// expectation: %q", got)
			}
			if shown := strings.Contains(got, token); shown != tc.wantToken {
				t.Errorf("token shown = %v, want %v (output %q)", shown, tc.wantToken, got)
			}
			logged := logs.String()
			if strings.Contains(logged, token) || strings.Contains(logged, "?token=") {
				t.Fatalf("the process log contains the token or the login link:\n%s", logged)
			}
		})
	}
}

// captureStreams swaps os.Stdout and os.Stderr for pipes around fn and returns
// what each received: the login notice may be written to only one of them, so
// pinning the stream requires capturing both.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	fn()
	os.Stdout, os.Stderr = prevOut, prevErr
	outW.Close()
	errW.Close()
	outBytes, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	errBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatal(err)
	}
	return string(outBytes), string(errBytes)
}

// runWebNotice runs the real `cask web` startup path with args and returns what
// it printed on stdout and stderr together with everything it logged.
func runWebNotice(t *testing.T, args ...string) (stdout, stderr, logged string) {
	t.Helper()
	t.Setenv(viewerTokenEnv, "")
	logs := installRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	var code int
	stdout, stderr = captureStreams(t, func() {
		code = runWeb(ctx, modeFlags{store: t.TempDir()}, append(args, "-bind", "127.0.0.1:0", "-no-open"))
	})
	if code != 0 {
		t.Fatalf("runWeb exit = %d, want 0 (stdout %q, log %q)", code, stdout, logs.String())
	}
	return stdout, stderr, logs.String()
}

// TestRunWebNoticeGoesToStdout is the stream contract of issue #212: the
// one-time login hint is command output, so it lands on stdout and never on
// stderr, which cli.md §3 reserves for errors and which supervisors retain.
func TestRunWebNoticeGoesToStdout(t *testing.T) {
	stdout, stderr, logged := runWebNotice(t, "-show-token")
	if !strings.Contains(stdout, "cask web: log in once at http://127.0.0.1:") {
		t.Fatalf("stdout does not carry the login notice: %q", stdout)
	}
	if !strings.Contains(stdout, "/viewer/?token=") {
		t.Fatalf("stdout does not carry the login deep link: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr carries output, want the notice on stdout only: %q", stderr)
	}
	if strings.Contains(logged, "/viewer/?token=") {
		t.Fatalf("the process log contains the login link:\n%s", logged)
	}
}

// TestRunWebShowTokenControlsTheDisplay covers both directions of the flag on
// the real startup path, where stdout is not a terminal (the test binary's
// stdout is a pipe): -show-token displays the one-time hint anyway, while an
// absent flag keeps the terminal heuristic and -show-token=false suppresses it
// even where the heuristic would have shown it.
func TestRunWebShowTokenControlsTheDisplay(t *testing.T) {
	t.Run("hidden by default without a terminal", func(t *testing.T) {
		stdout, _, logged := runWebNotice(t)
		if strings.Contains(stdout, "?token=") {
			t.Fatalf("the default run displayed the login link: %q", stdout)
		}
		if !strings.Contains(stdout, "cask web: viewer at http://127.0.0.1:") {
			t.Fatalf("the hidden notice does not name the viewer: %q", stdout)
		}
		if !strings.Contains(logged, "startup token was generated but not shown") {
			t.Fatalf("the hidden token logged no remedy:\n%s", logged)
		}
	})
	t.Run("forced without a terminal", func(t *testing.T) {
		stdout, _, _ := runWebNotice(t, "-show-token")
		if !strings.Contains(stdout, "?token=") {
			t.Fatalf("-show-token did not display the login link: %q", stdout)
		}
	})
	t.Run("suppressed", func(t *testing.T) {
		stdout, _, logged := runWebNotice(t, "-show-token=false")
		if strings.Contains(stdout, "?token=") {
			t.Fatalf("-show-token=false still displayed the login link: %q", stdout)
		}
		if strings.Contains(logged, "startup token was generated but not shown") {
			t.Fatalf("-show-token=false is the operator's choice and needs no remedy:\n%s", logged)
		}
	})
}

// TestRunWebRejectsInvalidShowToken: a value that is neither a bool nor absent
// is a usage error naming the flag, not a silently ignored flag (cli.md §3).
func TestRunWebRejectsInvalidShowToken(t *testing.T) {
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-show-token=maybe", "-no-open"}); code != 2 {
		t.Fatalf("runWeb -show-token=maybe exit = %d, want 2 (usage)", code)
	}
}

// TestResolveStartupTokenSources covers the unattended token sources of issue
// #179: a token read from -token-file or CASK_VIEWER_TOKEN is used as given and
// marked not generated (so it is never displayed), the flag wins over the
// environment, and a missing or empty file fails without echoing a token.
func TestResolveStartupTokenSources(t *testing.T) {
	const (
		fromFile = "FEED-FACE-0001"
		fromEnv  = "FEED-FACE-0002"
	)
	file := filepath.Join(t.TempDir(), "startup-token")
	if err := os.WriteFile(file, []byte(fromFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty-token")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "absent")

	t.Run("token file", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		logs := installRecorder(t)
		token, generated, err := resolveStartupToken(webArgs{tokenFile: file})
		if err != nil || token != fromFile || generated {
			t.Fatalf("resolveStartupToken(-token-file) = (%q, %v, %v), want (%q, false, nil)", token, generated, err, fromFile)
		}
		if logged := logs.String(); strings.Contains(logged, fromFile) {
			t.Fatalf("token resolution logged the token:\n%s", logged)
		}
	})

	t.Run("flag wins over the environment", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, fromEnv)
		token, generated, err := resolveStartupToken(webArgs{tokenFile: file})
		if err != nil || token != fromFile || generated {
			t.Fatalf("resolveStartupToken(-token-file, env) = (%q, %v, %v), want the file's token", token, generated, err)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "  "+fromEnv+"  ")
		logs := installRecorder(t)
		token, generated, err := resolveStartupToken(webArgs{})
		if err != nil || token != fromEnv || generated {
			t.Fatalf("resolveStartupToken(env) = (%q, %v, %v), want (%q, false, nil)", token, generated, err, fromEnv)
		}
		if logged := logs.String(); strings.Contains(logged, fromEnv) {
			t.Fatalf("token resolution logged the token:\n%s", logged)
		}
	})

	t.Run("generated when nothing is supplied", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		token, generated, err := resolveStartupToken(webArgs{})
		if err != nil || !generated {
			t.Fatalf("resolveStartupToken() = (%q, %v, %v), want a generated token", token, generated, err)
		}
		if len(token) != 14 {
			t.Fatalf("generated token %q is not the documented 3-group form", token)
		}
	})

	t.Run("unusable file is an error naming the file", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		for _, path := range []string{missing, empty} {
			token, generated, err := resolveStartupToken(webArgs{tokenFile: path})
			if err == nil || token != "" || generated {
				t.Fatalf("resolveStartupToken(%q) = (%q, %v, %v), want an error", path, token, generated, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not name the file %q", err, path)
			}
		}
	})
}

// TestRunWebNeverLogsSuppliedToken runs the real `cask web` startup path with an
// operator-supplied token and asserts the token reaches neither the process log
// (at any level) nor the announcement: an unattended deployment supplies its
// token instead of having it printed.
func TestRunWebNeverLogsSuppliedToken(t *testing.T) {
	const supplied = "DEAD-BEEF-1234"
	tokenFile := filepath.Join(t.TempDir(), "startup-token")
	if err := os.WriteFile(tokenFile, []byte(supplied), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := installRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	if code := runWeb(ctx, modeFlags{store: t.TempDir()}, []string{
		"-bind", "127.0.0.1:0", "-no-open", "-token-file", tokenFile,
	}); code != 0 {
		t.Fatalf("runWeb exit = %d, want 0", code)
	}
	if logged := logs.String(); strings.Contains(logged, supplied) {
		t.Fatalf("the process log contains the supplied startup token:\n%s", logged)
	}
}
