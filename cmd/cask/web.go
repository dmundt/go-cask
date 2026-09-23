package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
	"github.com/dmundt/go-cask/internal/web"
)

// webArgs holds the viewer's flag values.
type webArgs struct {
	store         string
	bind          string
	hashAlgorithm string
	tokens        string
	allowInsecure bool
	noOpen        bool
}

// webFlags registers the viewer's flags over a, defaulting -store to
// storeDefault (the global -store flag; cli.md §1). runWeb and the command
// table both use it, so the accepted and the documented flags are one set
// (cli.md §2, §4).
func webFlags(a *webArgs, storeDefault string) *flag.FlagSet {
	flags := newFlagSet("web")
	flags.StringVar(&a.store, "store", storeDefault, "filesystem store directory")
	flags.StringVar(&a.bind, "bind", "127.0.0.1:8080", "listen address")
	flags.StringVar(&a.hashAlgorithm, "hash-algo", sha256.Name, "digest algorithm: sha256, sha512, or sha512_256")
	flags.StringVar(&a.tokens, "tokens", "", "comma-separated role=token pairs for viewer login (e.g. admin=...,operator=...)")
	flags.BoolVar(&a.allowInsecure, "allow-insecure-bind", false, "allow a non-loopback bind without HTTPS")
	flags.BoolVar(&a.noOpen, "no-open", false, "do not open the default browser")
	return flags
}

// runWeb starts the embedded viewer — the product's only HTTP surface
// (backend-architecture §3). Invoking `cask web` IS the explicit enablement
// (viewer-security §3); binding to a non-loopback address requires explicit
// confirmation (viewer-security §4). The store defaults to the global -store
// flag when the subcommand's own -store is not given.
func runWeb(ctx context.Context, mf modeFlags, args []string) int {
	storeDefault := mf.store
	if storeDefault == "" {
		storeDefault = "./objects"
	}
	var a webArgs
	flags := webFlags(&a, storeDefault)
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

	backend, err := fsbackend.New(a.store)
	if err != nil {
		slog.Error("open store", "err", err)
		return 1
	}
	hasher, err := viewerHasher(a.hashAlgorithm)
	if err != nil {
		slog.Error("invalid viewer hash algorithm", "algorithm", a.hashAlgorithm, "err", err)
		return 2
	}
	references, err := previewReferences(ctx, backend)
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
	for _, pair := range strings.Split(a.tokens, ",") {
		role, tok, ok := strings.Cut(pair, "=")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			continue // ignore empty entries and tokens that would grant a role to ""
		}
		roleTokens[tok] = strings.TrimSpace(role)
	}

	token, err := randomToken()
	if err != nil {
		slog.Error("generate startup token", "err", err)
		return 1
	}
	slog.Warn("viewer startup token", "admin_token", token) // printed once, never stored
	viewerConfig := web.Config{
		Hasher:        hasher,
		HashAlgorithm: a.hashAlgorithm,
		StartupToken:  token,
		RoleTokens:    roleTokens,
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

	url := fmt.Sprintf("http://%s/viewer/?token=%s", listener.Addr(), token)
	fmt.Fprintf(os.Stderr, "cask web: %s\n", url)
	if !a.noOpen {
		openBrowser(url)
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
