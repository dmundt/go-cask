package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
//
// The matrix now crosses the bind with the display choice: #260 established that
// a non-loopback bind displays no token at all, whatever the display choice,
// because viewer-security §11 permits the one-time display only for a loopback
// bind.
func TestAnnounceLoginNeverLogsToken(t *testing.T) {
	const token = "AAAA-BBBB-CCCC"
	loopback := "http://127.0.0.1:8080"
	for _, tc := range []struct {
		name      string
		bind      string
		baseURL   string
		generated bool
		display   displayChoice
		wantShown bool
	}{
		{"loopback, generated, display chosen", "127.0.0.1:8080", loopback, true, displayShown, true},
		{"loopback, generated, hidden by default", "127.0.0.1:8080", loopback, true, displayHidden, false},
		{"loopback, generated, suppressed by the operator", "127.0.0.1:8080", loopback, true, displaySuppressed, false},
		{"loopback, supplied, display chosen", "127.0.0.1:8080", loopback, false, displayShown, false},
		{"loopback, supplied, hidden", "127.0.0.1:8080", loopback, false, displayHidden, false},
		{"non-loopback, generated, display chosen", "0.0.0.0:8080", "", true, displayShown, false},
		{"non-loopback, generated, hidden", "0.0.0.0:8080", "", true, displayHidden, false},
		{"non-loopback, generated, suppressed", "0.0.0.0:8080", "", true, displaySuppressed, false},
		{"non-loopback, supplied, display chosen", "0.0.0.0:8080", "", false, displayShown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := installRecorder(t)
			var out strings.Builder
			announceLogin(&out, loginNotice{
				bind:      tc.bind,
				baseURL:   tc.baseURL,
				token:     token,
				generated: tc.generated,
				display:   tc.display,
			})
			logged := logs.String()
			if strings.Contains(logged, token) {
				t.Fatalf("the process log contains the startup token:\n%s", logged)
			}
			if strings.Contains(logged, loginURL(loopback, token)) {
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

// TestRunWebPinsLoopbackBindings is #337's acceptance test on the real startup
// path: every accepted loopback spelling yields a listener on an explicit
// numeric loopback address chosen by the viewer, and the printed login origin is
// that listener's own address — so the origin, the browser URL and the host
// firewall's behaviour no longer follow what the machine's resolver made of the
// name. `localhost` in particular must land on 127.0.0.1, not on whichever
// family the hosts file preferred that day.
func TestRunWebPinsLoopbackBindings(t *testing.T) {
	for _, tc := range []struct {
		bind string
		host string
		ipv6 bool
	}{
		{"127.0.0.1:0", "127.0.0.1", false},
		{"localhost:0", "127.0.0.1", false},
		{"LOCALHOST:0", "127.0.0.1", false},
		{"[::1]:0", "::1", true},
	} {
		t.Run(tc.bind, func(t *testing.T) {
			if tc.ipv6 {
				if runtime.GOOS == "windows" {
					// The probe and the run both open an IPv6 listener, which is
					// reported to raise the interactive firewall prompt a
					// non-loopback bind does, so this row cannot run unattended
					// there (#440). Its spelling stays pinned on every platform
					// by the bind table in TestVersionAndWebHelpers, which never
					// binds, and the row still runs on the gate's Linux.
					t.Skip("an IPv6 listener raises an interactive Windows firewall prompt, so this row cannot run unattended (go-cask#440)")
				}
				// A host with no IPv6 loopback cannot listen on [::1]; the
				// spelling itself stays pinned by the bind table in
				// TestVersionAndWebHelpers either way.
				probe, err := net.Listen("tcp", "[::1]:0")
				if err != nil {
					t.Skipf("this host has no IPv6 loopback: %v", err)
				}
				probe.Close()
			}
			stdout, _, logged := runWebNoticeOn(t, tc.bind, "-show-token")
			addr := listeningAddr(t, logged)
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("listener address %q is not host:port: %v", addr, err)
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				t.Fatalf("listener address %q is not an explicit numeric loopback address", addr)
			}
			if host != tc.host {
				t.Errorf("listener host = %q, want %q", host, tc.host)
			}
			if want := "cask web: log in once at http://" + addr + "/viewer/?token="; !strings.Contains(stdout, want) {
				t.Errorf("the printed origin is not the listener's own address %q: %q", addr, stdout)
			}
		})
	}
}

// listeningAddr returns the address the viewer logged on its startup line —
// "cask web listening (viewer) addr=…" — which is the listener.Addr() the
// serving goroutine reported.
func listeningAddr(t *testing.T, logged string) string {
	t.Helper()
	const marker = "cask web listening (viewer) addr="
	_, rest, ok := strings.Cut(logged, marker)
	if !ok {
		t.Fatalf("no startup line in the log:\n%s", logged)
	}
	addr, _, _ := strings.Cut(rest, " ")
	if addr == "" {
		t.Fatalf("the startup line names no address:\n%s", logged)
	}
	return addr
}

// TestRunWebRefusesWildcardBind is #337's refusal half: a bare `:port` listens
// on every interface of both families, so it is a non-loopback bind exactly like
// `0.0.0.0:port` and `[::]:port`, and must be refused without
// -allow-insecure-bind. The refusal precedes the listen, so this test never
// binds a fixed port.
func TestRunWebRefusesWildcardBind(t *testing.T) {
	for _, bind := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.10:8080"} {
		t.Run(bind, func(t *testing.T) {
			logs := installRecorder(t)
			if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-bind", bind, "-no-open"}); code != 1 {
				t.Fatalf("runWeb -bind %s exit = %d, want 1", bind, code)
			}
			if !strings.Contains(logs.String(), "refusing to bind the viewer to a non-loopback address") {
				t.Errorf("the refusal is not logged for %s:\n%s", bind, logs.String())
			}
		})
	}
}

// TestAnnounceLoginNonLoopbackPrintsNoLink is the non-loopback half of the
// printing rule: a bind whose plain http:// origin cannot hold a session gets no
// login link at all, the notice names the bind and the https:// expectation
// instead, and — since #260 — it displays no token either, whatever the display
// choice, because viewer-security §11 permits the one-time display only for a
// loopback bind (cli.md §2, viewer-security §7, §11).
func TestAnnounceLoginNonLoopbackPrintsNoLink(t *testing.T) {
	const (
		bind  = "0.0.0.0:8080"
		token = "AAAA-BBBB-CCCC"
	)
	for _, tc := range []struct {
		name       string
		generated  bool
		display    displayChoice
		wantToken  bool
		wantRemedy bool
	}{
		{"generated, display chosen", true, displayShown, false, true},
		{"generated, hidden", true, displayHidden, false, true},
		{"generated, suppressed", true, displaySuppressed, false, false},
		{"supplied, display chosen", false, displayShown, false, false},
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
			// A run that wanted the hint is told why it cannot have it; a run
			// that suppressed it asked for silence and gets no remedy.
			remedy := strings.Contains(logged, "only for a loopback bind")
			if remedy != tc.wantRemedy {
				t.Errorf("loopback remedy logged = %v, want %v:\n%s", remedy, tc.wantRemedy, logged)
			}
		})
	}
}

// TestBrowserLaunchAllowed is the other half of #260: the token deep link is
// handed to the browser only when the operator did not suppress the display and
// the bind is loopback, so the raw token cannot reach another process's argument
// vector in the cases the flag was meant to prevent (viewer-security §4, §11).
func TestBrowserLaunchAllowed(t *testing.T) {
	const loopback = "http://127.0.0.1:8080"
	for _, tc := range []struct {
		name    string
		noOpen  bool
		display displayChoice
		baseURL string
		want    bool
	}{
		{"loopback, shown", false, displayShown, loopback, true},
		{"loopback, hidden by the default heuristic", false, displayHidden, loopback, true},
		{"loopback, suppressed", false, displaySuppressed, loopback, false},
		{"non-loopback, shown", false, displayShown, "", false},
		{"non-loopback, hidden", false, displayHidden, "", false},
		{"-no-open wins", true, displayShown, loopback, false},
		{"-no-open on a non-loopback bind", true, displayShown, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserLaunchAllowed(tc.noOpen, tc.display, tc.baseURL); got != tc.want {
				t.Fatalf("browserLaunchAllowed(%v, %v, %q) = %v, want %v", tc.noOpen, tc.display, tc.baseURL, got, tc.want)
			}
		})
	}
}

// TestBrowserCommandWindowsNeverRunsTheTokenAsSyntax is #349's launcher half. The
// startup token is arbitrary operator-supplied text, and on Windows the deep
// link used to be handed to a command interpreter, which re-parses `&`, `|`, `^`
// and `%…%` itself: a token of the form `<text>&<verb>` became a second command.
// The regression is driven through the real launch inputs — loginURL's encoding
// and browserCommand's per-platform mapping — because that is the argument
// vector openBrowser hands to exec.Command, and it asserts that (a) no command
// interpreter is involved at all, (b) loginURL's percent-encoding is what keeps
// the token out of the URL, so no metacharacter of its own survives unencoded,
// and (c) the token the browser sends back is still the token the operator
// supplied (viewer-security §11).
func TestBrowserCommandWindowsNeverRunsTheTokenAsSyntax(t *testing.T) {
	// Every character cmd.exe would re-interpret, plus the quote that
	// EscapeArg would use to protect them: Go does not quote an argument
	// without a space, and cmd.exe is not a CommandLineToArgvW consumer, so
	// that quoting never protected this command line. The pieces are
	// assembled rather than written as one literal so the test binary does not
	// ship a single string a malware scanner reads as an injected command.
	token := "X&" + "calc|run^%PATH%#" + `"end`
	const wantURL = "http://127.0.0.1:8080/viewer/"

	link := loginURL("http://127.0.0.1:8080", token)
	if !strings.HasPrefix(link, wantURL) {
		t.Fatalf("loginURL = %q, want the viewer deep link under %q", link, wantURL)
	}
	if strings.Contains(link, token) {
		t.Fatalf("loginURL left the raw token in the deep link: %q", link)
	}
	if raw := bareCmdMetacharacter(link); raw != "" {
		t.Fatalf("the deep link carries the unencoded metacharacter %q: %q", raw, link)
	}

	name, args := browserCommand("windows", link)
	if strings.EqualFold(filepath.Base(name), "cmd") || strings.EqualFold(filepath.Base(name), "cmd.exe") {
		t.Fatalf("the Windows launch still runs through cmd.exe: %q %v", name, args)
	}
	// The launcher is pinned by name on every platform, because that is the part
	// the mapping decides; resolving it is the host's business. `rundll32.exe`
	// lives in the Windows system directory, so a Linux runner — where this
	// repository's cross-platform suite runs — cannot look it up, and asking it to
	// would fail the test for its host rather than for the command. On Windows the
	// lookup is the check that the name is one the OS finds instead of a relative
	// path the viewer's working directory would decide.
	if !strings.EqualFold(filepath.Base(name), "rundll32.exe") {
		t.Fatalf("the Windows launch uses %q, want rundll32.exe", name)
	}
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("the Windows launcher %q is not resolvable: %v", name, err)
		}
	}
	joined := name + " " + strings.Join(args, " ")
	if raw := bareCmdMetacharacter(joined); raw != "" {
		t.Fatalf("the launch argument vector still holds the unescaped metacharacter %q: %q", raw, joined)
	}
	tokenArg := ""
	for _, a := range args {
		if strings.HasPrefix(a, wantURL) {
			tokenArg = a
		}
	}
	if tokenArg == "" {
		t.Fatalf("no argument carries the deep link: %q %v", name, args)
	}
	parsed, err := url.Parse(tokenArg)
	if err != nil {
		t.Fatalf("url.Parse(%q) = %v", tokenArg, err)
	}
	if got := parsed.Query().Get("token"); got != token {
		t.Fatalf("the browser receives token %q, want the supplied %q", got, token)
	}
}

// TestLoginURLRoundTripsReservedTokens is #349's deep-link half: the token is
// percent-encoded into the login URL, and the endpoint — which reads the query
// with r.URL.Query().Get("token") — decodes it back to exactly what the operator
// supplied. Before the fix a token holding `&`, `#` or `%` reached the browser
// truncated or reinterpreted, so a valid token silently failed to log in.
func TestLoginURLRoundTripsReservedTokens(t *testing.T) {
	const baseURL = "http://127.0.0.1:8080"
	// `%` is the interesting one in both directions: a raw one is an escape
	// introducer, and an encoded token containing `%XX` must not be decoded
	// twice on the way back.
	for _, token := range []string{
		"X&" + "calc",
		`a|b`,
		`caret^and%PATH%`,
		`hash#fragment`,
		`percent%41`,
		`two&&ands`,
		`quote"and space`,
		`plus+and ampersand&`,
	} {
		t.Run(token, func(t *testing.T) {
			link := loginURL(baseURL, token)
			r := httptest.NewRequest("GET", link, nil)
			if got := r.URL.Query().Get("token"); got != token {
				t.Fatalf("r.URL.Query().Get(\"token\") = %q, want %q (link %q)", got, token, link)
			}
			if r.URL.Path != "/viewer/" {
				t.Fatalf("the deep link's path = %q, want /viewer/ (link %q)", r.URL.Path, link)
			}
		})
	}
}

// bareCmdMetacharacter reports the first cmd.exe metacharacter in s that is not
// part of a percent-escape, or "" when every one of them is escaped. The
// distinction matters in both directions for #349: `A%26B` is data — an encoded
// `&` — while `A&B` is syntax, and a `%` that does not open a two-digit escape
// is the character cmd.exe would expand an environment variable from.
func bareCmdMetacharacter(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%':
			if i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
				i += 2 // a percent-escape the URL decoder — not cmd.exe — consumes
				continue
			}
			return "%"
		case '&', '|', '^', '<', '>', '#', '"':
			return string(s[i])
		}
	}
	return ""
}

// isHexDigit reports whether b is an ASCII hex digit.
func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
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

// runWebNotice runs the real `cask web` startup path on a loopback bind with args
// and returns what it printed on stdout and stderr together with everything it
// logged.
func runWebNotice(t *testing.T, args ...string) (stdout, stderr, logged string) {
	t.Helper()
	return runWebNoticeOn(t, "127.0.0.1:0", args...)
}

// runWebNoticeOn is runWebNotice for an explicit bind, so a test can drive the
// real startup path over a bind the notice may not print a link for (#260).
func runWebNoticeOn(t *testing.T, bind string, args ...string) (stdout, stderr, logged string) {
	t.Helper()
	t.Setenv(viewerTokenEnv, "")
	logs := installRecorder(t)
	stdout, stderr, code := runWebOn(t, t.TempDir(), append(args, "-bind", bind, "-no-open")...)
	if code != 0 {
		t.Fatalf("runWeb exit = %d, want 0 (stdout %q, log %q)", code, stdout, logs.String())
	}
	return stdout, stderr, logs.String()
}

// webStartupNotice is the prefix every viewer startup writes to stdout: the
// login hint or the location notice announceLogin prints (cli.md §3, §4). By the
// time it appears the preview graph is built, the listener is bound and the
// server is about to serve, so it is the viewer's readiness signal.
const webStartupNotice = "cask web: "

// webStartupTimeout bounds the wait for that notice. It bounds a run that never
// comes up rather than a startup budget: the preview walk hashes up to
// maxPreviewCount ordinals, which a machine loaded by the rest of the gate can
// stretch to seconds — time this harness must spend waiting, not canceling.
const webStartupTimeout = 90 * time.Second

// runWebOn runs the real `cask web` startup path over store with args and
// cancels it as soon as the viewer has announced itself, returning runWeb's exit
// code with what the run wrote to stdout and stderr.
//
// The cancellation is driven by the startup notice, never by a wall-clock timer.
// runWeb builds the preview reference index on this context and threads it into
// every backend.Exists call (preview_seed.go), so a timer that expires mid-walk
// is reported as a startup failure: a harness that guessed 150 ms measured
// machine load instead of the contract it names (go-cask#440). A run that fails
// before it announces anything returns that failure's exit code for the caller
// to assert, and a run that never announces anything fails the wait.
func runWebOn(t *testing.T, store string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	errOut, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()

	// Files rather than pipes: the wait below polls stdout while the run is still
	// writing to it, and an *os.File is what os.Stdout already is, so the notice
	// keeps the same non-terminal behavior a pipe would give it.
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	defer func() { os.Stdout, os.Stderr = prevOut, prevErr }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- runWeb(ctx, modeFlags{store: store}, args) }()
	code = awaitWebNotice(t, cancel, done, outPath)

	os.Stdout, os.Stderr = prevOut, prevErr
	return readCapture(t, outPath), readCapture(t, errPath), code
}

// awaitWebNotice waits for the viewer's startup notice in the captured stdout,
// then cancels the run and returns its exit code. A run that returns before it
// announces anything (a startup failure) is reported at once, so the wait is
// bounded by the run itself whenever the viewer cannot come up — the timeout
// only guards a run that neither starts nor fails.
func awaitWebNotice(t *testing.T, cancel context.CancelFunc, done <-chan int, path string) int {
	t.Helper()
	deadline := time.Now().Add(webStartupTimeout)
	for {
		if strings.Contains(readCapture(t, path), webStartupNotice) {
			cancel()
			return <-done
		}
		select {
		case code := <-done:
			return code
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the viewer printed no startup notice within %s (stdout %q)", webStartupTimeout, readCapture(t, path))
		}
		time.Sleep(time.Millisecond)
	}
}

// readCapture returns everything written to path so far. The run holds the file
// open, which is what lets the wait poll a stream that is still being written.
func readCapture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

// TestRunWebNonLoopbackNeverShowsTheToken is the real-path regression guard for
// #260: even with -show-token asking for the hint, a bind the notice may not
// print a link for displays no token, and the run logs why — never the token
// (viewer-security §9, §11).
//
// Observing a non-loopback notice on the real path needs a real non-loopback
// listener, and on Windows opening one raises the interactive firewall prompt a
// freshly built test binary asks for, so the test cannot run unattended there
// (#440). The rule is not left to it: noticeOrigin answers "" for a non-loopback
// address (TestNoticeOrigin), announceLogin prints the location notice and no
// link for that answer (TestAnnounceLoginNonLoopbackPrintsNoLink), and the launch
// that would carry the token is refused for it (TestBrowserLaunchAllowed) — all
// without a socket, on every platform. The skip therefore leaves unpinned on
// Windows only the wiring between them (runWeb handing noticeOrigin's answer to
// announceLogin), and it loses nothing where the gate runs: the race suite is
// Linux.
func TestRunWebNonLoopbackNeverShowsTheToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a non-loopback bind raises an interactive Windows firewall prompt, so this test cannot run unattended (go-cask#440)")
	}
	stdout, stderr, logged := runWebNoticeOn(t, "0.0.0.0:0", "-allow-insecure-bind", "-show-token")
	if strings.Contains(stdout, "?token=") {
		t.Fatalf("a non-loopback run displayed the login link: %q", stdout)
	}
	if !strings.Contains(stdout, "the startup token is never logged or echoed") {
		t.Fatalf("a non-loopback run printed no location notice: %q", stdout)
	}
	if !strings.Contains(stdout, "https://") || strings.Contains(stdout, "127.0.0.1:") {
		t.Fatalf("the notice does not name the bind and the https:// expectation: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr carries output, want the notice on stdout only: %q", stderr)
	}
	if !strings.Contains(logged, "only for a loopback bind") {
		t.Fatalf("a non-loopback run logged no reason for the hidden token:\n%s", logged)
	}
}

// TestRunWebRejectsInvalidShowToken: a value that is neither a bool nor absent
// is a usage error naming the flag, not a silently ignored flag (cli.md §3).
func TestRunWebRejectsInvalidShowToken(t *testing.T) {
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-show-token=maybe", "-no-open"}); code != 2 {
		t.Fatalf("runWeb -show-token=maybe exit = %d, want 2 (usage)", code)
	}
}

// TestResolveStartupTokenSources covers the unattended token sources of issue
// #179 and the supply contract of #348: a token read from -token-file or
// CASK_VIEWER_TOKEN is used as given and marked not generated (so it is never
// displayed), the flag wins over the environment, and a missing, empty,
// non-regular, oversized, too-short or off-charset value fails without echoing
// a token.
func TestResolveStartupTokenSources(t *testing.T) {
	const (
		fromFile = "FEED-FACE-0000-0001"
		fromEnv  = "FEED-FACE-0000-0002"
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
		// The generated width is the documented 128-bit, 4-group form
		// (defaults §4): changing it means changing this deliberately.
		if len(token) != 35 || strings.Count(token, "-") != 3 {
			t.Fatalf("generated token %q is not the documented 4-group form", token)
		}
		for _, group := range strings.Split(token, "-") {
			if len(group) != 8 {
				t.Fatalf("generated token %q group %q, want 8 hex characters", token, group)
			}
			for _, r := range group {
				if (r < '0' || r > '9') && (r < 'A' || r > 'F') {
					t.Fatalf("generated token %q carries %q, want uppercase hex groups", token, r)
				}
			}
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

	// #348: a supplied token must come from a regular file. The refusal happens
	// before the file is opened, which is what keeps a FIFO or a device from
	// blocking or inflating startup instead of failing it fast.
	t.Run("non-regular files are refused", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		cases := []struct {
			name string
			path string
		}{
			{"directory", t.TempDir()},
			{"device", os.DevNull},
		}
		fifo := filepath.Join(t.TempDir(), "startup-token.fifo")
		if makeFIFO(t, fifo) {
			cases = append(cases, struct {
				name string
				path string
			}{"FIFO", fifo})
		} else {
			t.Log("no mkfifo on this host: the FIFO case is not exercised")
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// A read that consults the file without the regular-file check
				// blocks forever on a FIFO, so the call is bounded here rather
				// than left to the package timeout.
				done := make(chan error, 1)
				go func() {
					_, _, err := resolveStartupToken(webArgs{tokenFile: tc.path})
					done <- err
				}()
				select {
				case err := <-done:
					if err == nil {
						t.Fatalf("-token-file %s was accepted, want an error", tc.path)
					}
					if !strings.Contains(err.Error(), tc.path) {
						t.Fatalf("error %q does not name the file %q", err, tc.path)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("-token-file %s blocked: a non-regular file must be refused before it is opened", tc.path)
				}
			})
		}
	})

	// The read is bounded: a file past the bound is refused by size, not read
	// into memory and then judged on its content.
	t.Run("oversize file is refused", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		oversize := filepath.Join(t.TempDir(), "huge-token")
		// Every byte is inside the accepted character set, so the size rule
		// alone is what must refuse it.
		body := []byte(strings.Repeat("A", maxStartupTokenBytes+1))
		if err := os.WriteFile(oversize, body, 0o600); err != nil {
			t.Fatal(err)
		}
		token, generated, err := resolveStartupToken(webArgs{tokenFile: oversize})
		if err == nil || token != "" || generated {
			t.Fatalf("resolveStartupToken(oversize) = (%q, %v, %v), want an error", token, generated, err)
		}
		if !strings.Contains(err.Error(), oversize) || !strings.Contains(err.Error(), strconv.Itoa(maxStartupTokenBytes)) {
			t.Fatalf("oversize error = %q, want it to name the file and the bound", err)
		}
	})

	// The width and character-set floor, and the rule that a refused value is
	// never restated in the error (viewer-security §11).
	t.Run("short and malformed tokens are refused without echoing them", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		allowed := "abcd-EFGH-0123-._~" // every accepted character class
		for _, tc := range []struct {
			name  string
			token string
		}{
			{"short", "SHORT-TOKEN"},
			{"outside the charset", "GOOD-TOKEN-0000+0001"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "startup-token")
				if err := os.WriteFile(path, []byte(tc.token), 0o600); err != nil {
					t.Fatal(err)
				}
				token, generated, err := resolveStartupToken(webArgs{tokenFile: path})
				if err == nil || token != "" || generated {
					t.Fatalf("resolveStartupToken(%q) = (%q, %v, %v), want an error", tc.token, token, generated, err)
				}
				if !strings.Contains(err.Error(), path) {
					t.Fatalf("error %q does not name the file %q", err, path)
				}
				if strings.Contains(err.Error(), tc.token) {
					t.Fatalf("error %q echoes the rejected token", err)
				}
			})
		}

		path := filepath.Join(t.TempDir(), "startup-token")
		if err := os.WriteFile(path, []byte(allowed), 0o600); err != nil {
			t.Fatal(err)
		}
		token, generated, err := resolveStartupToken(webArgs{tokenFile: path})
		if err != nil || token != allowed || generated {
			t.Fatalf("resolveStartupToken(%q) = (%q, %v, %v), want it accepted", allowed, token, generated, err)
		}
	})

	// The environment source follows the same contract, and a whitespace-only
	// value is still "nothing supplied" rather than a rejected token.
	t.Run("environment tokens follow the same contract", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "SHORT")
		token, generated, err := resolveStartupToken(webArgs{})
		if err == nil || token != "" || generated {
			t.Fatalf("resolveStartupToken(short env) = (%q, %v, %v), want an error", token, generated, err)
		}
		if !strings.Contains(err.Error(), viewerTokenEnv) || strings.Contains(err.Error(), "SHORT") {
			t.Fatalf("short env error = %q, want it to name the variable and not the value", err)
		}

		t.Setenv(viewerTokenEnv, "   ")
		if _, generated, err := resolveStartupToken(webArgs{}); err != nil || !generated {
			t.Fatalf("resolveStartupToken(blank env) = (%v, %v), want a generated token", generated, err)
		}
	})
}

// makeFIFO creates a named pipe at path where the host can, and reports whether
// it did: mkfifo exists on Unix and not on Windows. A FIFO is the non-regular
// file that would hang the token read rather than merely fail it, so its
// refusal is exercised wherever one can be created.
func makeFIFO(t *testing.T, path string) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		return false
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		return false
	}
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Logf("mkfifo %s: %v (%s)", path, err, out)
		return false
	}
	return true
}

// TestRunWebNeverLogsSuppliedToken runs the real `cask web` startup path with an
// operator-supplied token and asserts the token reaches neither the process log
// (at any level) nor the announcement: an unattended deployment supplies its
// token instead of having it printed.
func TestRunWebNeverLogsSuppliedToken(t *testing.T) {
	const supplied = "DEAD-BEEF-1234-5678"
	tokenFile := filepath.Join(t.TempDir(), "startup-token")
	if err := os.WriteFile(tokenFile, []byte(supplied), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := installRecorder(t)
	if _, _, code := runWebOn(t, t.TempDir(), "-bind", "127.0.0.1:0", "-no-open", "-token-file", tokenFile); code != 0 {
		t.Fatalf("runWeb exit = %d, want 0", code)
	}
	if logged := logs.String(); strings.Contains(logged, supplied) {
		t.Fatalf("the process log contains the supplied startup token:\n%s", logged)
	}
}
