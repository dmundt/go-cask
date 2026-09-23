package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
	"github.com/dmundt/go-cask/internal/store"
	"github.com/dmundt/go-cask/internal/web"
)

// webArgs holds the viewer's flag values.
type webArgs struct {
	store         string
	backend       string
	bind          string
	hashAlgorithm string
	tokens        string
	tokenFile     string
	trustedProxy  string
	allowInsecure bool
	showToken     tokenDisplay
	noOpen        bool
}

// displayChoice is the resolved answer to "may the viewer display the one-time
// login hint?". It has three states because the flag's absence is not a
// negation: an absent -show-token keeps the terminal heuristic, the flag forces
// the hint, and -show-token=false suppresses it for good (cli.md §2).
type displayChoice int

const (
	// displayHidden keeps the generated token off the notice because nothing
	// asked for it and the notice's stream is not an interactive terminal; the
	// remedy is logged.
	displayHidden displayChoice = iota
	// displayShown displays the generated token: the operator asked for it, or
	// the notice's stream is an interactive terminal and no choice was given.
	displayShown
	// displaySuppressed keeps the token off the notice because the operator
	// asked for that (-show-token=false), so no remedy is needed.
	displaySuppressed
)

// tokenDisplay is the -show-token flag: a tri-state choice, because its default
// is neither "always" nor "never" but the terminal heuristic, and the flag
// package's bool cannot tell an absent flag from an explicit
// -show-token=false (cli.md §2, §4).
type tokenDisplay struct {
	set   bool
	value bool
}

// String reports the flag's current state for help output.
func (d *tokenDisplay) String() string {
	if !d.set {
		return "auto"
	}
	return strconv.FormatBool(d.value)
}

// Set implements flag.Value. IsBoolFlag makes a bare -show-token mean
// -show-token=true, and -show-token=false its negation.
func (d *tokenDisplay) Set(value string) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("not a boolean: %q", value)
	}
	d.set, d.value = true, v
	return nil
}

// IsBoolFlag implements the flag package's boolFlag interface, so the flag may
// be written bare.
func (d *tokenDisplay) IsBoolFlag() bool { return true }

// resolve turns the flag and the notice stream's terminal state into the
// announcement's display choice: an absent flag keeps the terminal heuristic —
// the behavior before the flag existed — an explicit true forces the hint, and
// an explicit false suppresses it.
func (d tokenDisplay) resolve(interactive bool) displayChoice {
	switch {
	case !d.set:
		if interactive {
			return displayShown
		}
		return displayHidden
	case d.value:
		return displayShown
	default:
		return displaySuppressed
	}
}

// viewerTokenEnv supplies the viewer's startup admin token to an unattended
// deployment, so a service never needs the token printed (viewer-security §11).
const viewerTokenEnv = "CASK_VIEWER_TOKEN"

// webFlags registers the viewer's flags over a, defaulting -store to
// storeDefault and -backend to backendDefault (the global flags; cli.md §1).
// runWeb and the command table both use it, so the accepted and the documented
// flags are one set (cli.md §2, §4).
func webFlags(a *webArgs, storeDefault, backendDefault string) *flag.FlagSet {
	flags := newFlagSet("web")
	flags.StringVar(&a.store, "store", storeDefault, "filesystem store directory")
	flags.StringVar(&a.backend, "backend", backendDefault, "storage backend: fs (the viewer needs the filesystem backend)")
	flags.StringVar(&a.bind, "bind", "127.0.0.1:8080", "listen address")
	flags.StringVar(&a.hashAlgorithm, "hash-algo", sha256.Name, "digest algorithm: sha256, sha512, or sha512_256")
	flags.StringVar(&a.tokens, "tokens", "", "comma-separated role=token pairs for viewer login (e.g. admin=...,operator=...)")
	flags.StringVar(&a.tokenFile, "token-file", "", "file holding the startup admin token (read instead of generating one; never printed)")
	flags.StringVar(&a.trustedProxy, "trusted-proxy", "", "comma-separated IPs/CIDRs whose forwarded client address the login throttle may believe (e.g. 10.0.0.0/8); empty trusts none")
	flags.BoolVar(&a.allowInsecure, "allow-insecure-bind", false, "allow a non-loopback bind without HTTPS")
	flags.Var(&a.showToken, "show-token", "show the generated startup token's one-time login hint even when stdout is not an interactive terminal (default: only on an interactive stdout; -show-token=false never shows it)")
	flags.BoolVar(&a.noOpen, "no-open", false, "do not open the default browser")
	return flags
}

// splitList splits a comma-separated flag value into its trimmed, non-empty
// entries. Values that are not IP addresses or CIDR blocks are left for the
// viewer to reject at startup, so the error names the offending value rather
// than dropping it here.
func splitList(value string) []string {
	var entries []string
	for entry := range strings.SplitSeq(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// runWeb starts the embedded viewer — the product's only HTTP surface
// (backend-architecture §3). Invoking `cask web` IS the explicit enablement
// (viewer-security §3); binding to a non-loopback address requires explicit
// confirmation (viewer-security §4). The store defaults to the global -store
// flag when the subcommand's own -store is not given, and the backend to the
// global -backend for the same reason.
func runWeb(ctx context.Context, mf modeFlags, args []string) int {
	storeDefault := mf.store
	if storeDefault == "" {
		storeDefault = "./objects"
	}
	var a webArgs
	flags := webFlags(&a, storeDefault, mf.backend)
	if err := parseFlags(flags, args); err != nil {
		return reportError(err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !isLoopbackBind(a.bind) {
		if !a.allowInsecure {
			slog.Error("refusing to bind the viewer to a non-loopback address without HTTPS; set -allow-insecure-bind to override")
			return 1
		}
		// The session cookie is always Secure (viewer-security §7) and callers
		// cannot disable that, so a browser reaching this bind over plain
		// http:// will discard the cookie and never hold a session. The
		// override therefore needs a TLS-terminating proxy in front of it to
		// be usable at all — say so rather than let login fail silently.
		slog.Warn("viewer bound to a non-loopback address",
			"bind", a.bind,
			"note", "session cookies are always Secure, so log in over https:// (put a TLS-terminating proxy in front of this address); plain http:// logins will not hold a session")
	}

	kind, err := store.ParseKind(a.backend)
	if err != nil {
		slog.Error("invalid viewer backend", "backend", a.backend, "err", err)
		return 2
	}
	// The viewer needs the concrete filesystem backend (it reads per-object
	// physical metadata), so a backend with no filesystem view is refused with
	// an error naming the operation and the backend — never by silently
	// reading a different directory than -store named (cli.md §2).
	backend, err := store.OpenViewer(ctx, store.Options{Kind: kind, Path: a.store})
	if err != nil {
		slog.Error("open store", "backend", kind, "err", err)
		return 1
	}
	hasher, err := viewerHasher(a.hashAlgorithm)
	if err != nil {
		slog.Error("invalid viewer hash algorithm", "algorithm", a.hashAlgorithm, "err", err)
		return 2
	}
	references, err := previewReferences(ctx, backend, hasher)
	if err != nil && !errors.Is(err, errNoPreviewGraph) {
		slog.Error("build preview references", "err", err)
		return 1
	}
	// A store without the known deterministic preview graph is an ordinary
	// store and stays reference-free (cli.md §2): errNoPreviewGraph means the
	// preview walk found no preview object, not that it failed.
	hasPreviewGraph := err == nil
	// The viewer does not hold the store lock: its mutations are in-process
	// on its own store instance, and writers/reads are lock-free across
	// processes. External maintenance sweeps (cask gc/prune) may run while
	// the viewer is live — their grace `--min-age` keeps recent objects safe
	// (cas-core §6).
	roleTokens := map[string]string{} // token → role, for viewer login
	for pair := range strings.SplitSeq(a.tokens, ",") {
		role, tok, ok := strings.Cut(pair, "=")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			continue // ignore empty entries and tokens that would grant a role to ""
		}
		roleTokens[tok] = strings.TrimSpace(role)
	}

	token, generated, err := resolveStartupToken(a)
	if err != nil {
		slog.Error("viewer startup token", "err", err)
		return 1
	}
	viewerConfig := web.Config{
		Hasher:        hasher,
		HashAlgorithm: a.hashAlgorithm,
		StartupToken:  token,
		RoleTokens:    roleTokens,
		// The login throttle keys on the caller address. Empty — the default —
		// trusts no proxy, so a forwarded header is ignored; a configured
		// proxy's forwarded client address is believed instead
		// (viewer-security §5.2). A malformed entry fails web.New below.
		TrustedProxies: splitList(a.trustedProxy),
	}
	// A store without the known deterministic preview graph is an ordinary
	// store, and stays reference-free (cli.md §2).
	if hasPreviewGraph {
		viewerConfig.References = references
		viewerConfig.Reachability = references
	}
	webSrv, err := web.New(backend, viewerConfig)
	if err != nil {
		slog.Error("viewer setup", "err", err)
		return 1
	}

	root := http.NewServeMux()
	root.Handle("/viewer/", webSrv.Handler())
	root.Handle("/viewer", webSrv.Handler())

	httpSrv := &http.Server{
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", a.bind)
	if err != nil {
		slog.Error("listen", "addr", a.bind, "err", err)
		return 1
	}
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("cask web listening (viewer)", "addr", listener.Addr(), "store", a.store)
		if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			serverErr <- err
		}
	}()

	addr := listener.Addr().String()
	// The notice goes to stdout, the stream that carries command output, and
	// may be requested without a terminal; the deep link it prints is the one
	// that can log in (noticeOrigin). Opening the browser keeps the bind's
	// plain http:// origin it always used.
	announceLogin(os.Stdout, loginNotice{
		bind:      addr,
		baseURL:   noticeOrigin(addr),
		token:     token,
		generated: generated,
		display:   a.showToken.resolve(stdoutIsTerminal()),
	})
	if !a.noOpen {
		openBrowser(loginURL("http://"+addr, token))
	}

	exitCode := 0
	select {
	case <-ctx.Done():
	case <-serverErr:
		exitCode = 1
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
		if closeErr := httpSrv.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			slog.Error("force close", "err", closeErr)
		}
		return 1
	}
	return exitCode
}

// loginURL is the documented direct-token login deep link (viewer-security
// §5.1). It is the only URL that carries the token, and it is never logged.
func loginURL(baseURL, token string) string {
	return baseURL + "/viewer/?token=" + token
}

// resolveStartupToken returns the viewer's startup admin token and reports
// whether this run generated it. An operator-supplied token — `-token-file`
// first, then CASK_VIEWER_TOKEN — is used as given and is never displayed, so
// an unattended deployment never needs the token printed; only a token
// generated here may be shown, and only when the operator asks for it or the
// notice's stream is an interactive terminal (viewer-security §5.1, §11). A
// returned error names the flag or the file, not the token.
func resolveStartupToken(a webArgs) (string, bool, error) {
	if a.tokenFile != "" {
		b, err := os.ReadFile(a.tokenFile)
		if err != nil {
			return "", false, fmt.Errorf("read -token-file: %w", err)
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", false, fmt.Errorf("-token-file %s holds no token", a.tokenFile)
		}
		return token, false, nil
	}
	if token := strings.TrimSpace(os.Getenv(viewerTokenEnv)); token != "" {
		return token, false, nil
	}
	token, err := randomToken()
	if err != nil {
		return "", false, fmt.Errorf("generate startup token: %w", err)
	}
	return token, true, nil
}

// noticeOrigin returns the login deep link's origin for the startup notice, or
// "" when no link may be printed. Only a loopback bind can hold a plain http://
// login: the session cookie is always Secure (viewer-security §7), so a
// non-loopback bind is reachable only over https:// through a TLS-terminating
// proxy, and its bind address is not the origin the operator's browser uses
// (cli.md §2).
func noticeOrigin(listenerAddr string) string {
	if !isLoopbackBind(listenerAddr) {
		return ""
	}
	return "http://" + listenerAddr
}

// loginNotice is the viewer's one-time startup announcement: where the viewer
// can be reached, whether a login link can work there, and whether the
// generated startup token may be displayed.
type loginNotice struct {
	// bind is the address the viewer actually listens on; it names the
	// reachable location when no deep link can be printed.
	bind string
	// baseURL is the login deep link's origin, or empty when the bind is not
	// loopback (noticeOrigin).
	baseURL string
	// token is the startup admin token.
	token string
	// generated reports that this run generated the token. Only a generated
	// token may ever be displayed; an operator-supplied one is not repeated
	// (viewer-security §5.1, §11).
	generated bool
	// display is the resolved display choice.
	display displayChoice
}

// stdoutIsTerminal reports whether the notice's stream — standard output — is
// an interactive terminal, which is where a generated startup token may be
// shown by default. os.ModeCharDevice is the standard library's terminal test
// on every platform, so the viewer needs no terminal dependency
// (coding-guidelines §3). The test follows the stream the notice uses, not
// stderr: a run that redirects its notice into a file or a pipe must not have
// the token written into it (viewer-security §5.1, §11).
func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// announceLogin performs the viewer's one-time login announcement on w, the
// process's standard output: the hint is deliberate command output, so it
// belongs on the stream that carries command output while stderr stays reserved
// for errors (cli.md §3, §4).
//
// The token is written only when this run generated it AND display allows it; an
// operator-supplied token is not repeated. A deep link is written only when the
// bind can hold a login (loginNotice.baseURL); otherwise the notice names the
// bind and the https:// expectation, so a link that cannot log anyone in is
// never printed. A generated token that stays hidden logs the remedy — never the
// token, and never the link (viewer-security §5.1, §9, §11); the tests assert
// exactly that.
func announceLogin(w io.Writer, n loginNotice) {
	shown := n.generated && n.display == displayShown
	switch {
	case shown && n.baseURL != "":
		fmt.Fprintf(w, "cask web: log in once at %s (shown here only; the startup token is never logged)\n", loginURL(n.baseURL, n.token))
	case shown:
		fmt.Fprintf(w, "cask web: startup token %s (shown here only; the startup token is never logged)\n", n.token)
		fmt.Fprintf(w, "cask web: %s\n", viewerLocation(n))
	default:
		fmt.Fprintf(w, "cask web: %s — the startup token is never logged or echoed\n", viewerLocation(n))
	}
	if n.generated && n.display == displayHidden {
		slog.Warn("viewer startup token was generated but not shown: the notice stream is not an interactive terminal",
			"remedy", "run with -show-token to display the one-time login hint, or supply the token with -token-file or "+viewerTokenEnv)
	}
}

// viewerLocation names where the operator reaches the viewer: the deep link's
// origin for a loopback bind, or the bind and the https:// expectation for a
// non-loopback one, where no plain http:// link can hold a session
// (viewer-security §7).
func viewerLocation(n loginNotice) string {
	if n.baseURL != "" {
		return "viewer at " + n.baseURL + "/viewer/"
	}
	return "viewer bound to " + n.bind + ": log in over https:// through a TLS-terminating proxy (session cookies are always Secure, so this bind's plain http:// cannot hold a session)"
}

func viewerHasher(name string) (cas.Hasher, error) {
	switch name {
	case sha256.Name:
		return sha256.New(), nil
	case sha512.Name:
		return sha512.New(), nil
	case sha512256.Name:
		return sha512256.New(), nil
	default:
		return nil, fmt.Errorf("supported values are %q, %q, and %q", sha256.Name, sha512.Name, sha512256.Name)
	}
}

// isLoopbackBind reports whether the bind address is loopback.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host == "localhost"
	}
	return ip.IsLoopback()
}

// browserCommand returns the command that opens a URL in the default browser
// on the named GOOS. It is split from openBrowser so the per-platform mapping
// can be tested without launching a browser on the test machine.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		return "cmd", []string{"/c", "start", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// openBrowser opens the default browser to the given URL (cross-platform).
// Opening the browser is best-effort: a failure is logged and never fails the
// viewer. A successful Start is always reaped with Wait, which os/exec requires
// to release the child's resources.
func openBrowser(url string) {
	name, args := browserCommand(runtime.GOOS, url)
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		slog.Debug("open browser", "err", err) // not fatal
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Debug("wait for browser", "err", err) // not fatal
		}
	}()
}

// randomToken returns 6 cryptographically random bytes as uppercase
// dash-separated hex groups (viewer-security §5.1: startup token). A failure of
// the system random source is returned so the caller can report it and exit 1;
// a helper never panics (cli.md §3).
func randomToken() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read startup token: %w", err)
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12], nil
}
