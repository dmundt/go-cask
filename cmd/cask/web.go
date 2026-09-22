package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// runWeb starts the embedded viewer — the product's only HTTP surface
// (backend-architecture §3). Invoking `cask web` IS the explicit enablement
// (viewer-security §3); binding to a non-loopback address requires explicit
// confirmation (viewer-security §4). The store defaults to the global -store
// flag when the subcommand's own -store is not given.
func runWeb(ctx context.Context, mf modeFlags, args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	storeDefault := mf.store
	if storeDefault == "" {
		storeDefault = "./objects"
	}
	store := fs.String("store", storeDefault, "filesystem store directory")
	bind := fs.String("bind", "127.0.0.1:8080", "listen address")
	hashAlgorithm := fs.String("hash-algo", sha256.Name, "digest algorithm: sha256, sha512, or sha512_256")
	tokens := fs.String("tokens", "", "comma-separated role=token pairs for viewer login (e.g. admin=...,operator=...)")
	allowInsecure := fs.Bool("allow-insecure-bind", false, "allow a non-loopback bind without HTTPS")
	noOpen := fs.Bool("no-open", false, "do not open the default browser")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !isLoopbackBind(*bind) {
		if !*allowInsecure {
			slog.Error("refusing to bind the viewer to a non-loopback address without HTTPS; set -allow-insecure-bind to override")
			os.Exit(1)
		}
		// The session cookie is always Secure (viewer-security §7) and callers
		// cannot disable that, so a browser reaching this bind over plain
		// http:// will discard the cookie and never hold a session. The
		// override therefore needs a TLS-terminating proxy in front of it to
		// be usable at all — say so rather than let login fail silently.
		slog.Warn("viewer bound to a non-loopback address",
			"bind", *bind,
			"note", "session cookies are always Secure, so log in over https:// (put a TLS-terminating proxy in front of this address); plain http:// logins will not hold a session")
	}

	raw, err := fsbackend.New(*store)
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	hasher, err := viewerHasher(*hashAlgorithm)
	if err != nil {
		slog.Error("invalid viewer hash algorithm", "algorithm", *hashAlgorithm, "err", err)
		os.Exit(2)
	}
	references, err := previewReferences(ctx, raw)
	if err != nil {
		slog.Error("build preview references", "err", err)
		os.Exit(1)
	}
	// The viewer does not hold the store lock: its mutations are in-process
	// on its own store instance, and writers/reads are lock-free across
	// processes. External maintenance sweeps (cask gc/prune) may run while
	// the viewer is live — their grace `--min-age` keeps recent objects safe
	// (cas-core §6).
	roleTokens := map[string]string{} // token → role, for viewer login
	for _, pair := range strings.Split(*tokens, ",") {
		role, tok, ok := strings.Cut(pair, "=")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			continue // ignore empty entries and tokens that would grant a role to ""
		}
		roleTokens[tok] = strings.TrimSpace(role)
	}

	token := randomToken()
	slog.Warn("viewer startup token", "admin_token", token) // printed once, never stored
	viewerConfig := web.Config{
		Hasher:        hasher,
		HashAlgorithm: *hashAlgorithm,
		StartupToken:  token,
		RoleTokens:    roleTokens,
	}
	if references != nil {
		viewerConfig.References = references
		viewerConfig.Reachability = references
	}
	webSrv, err := web.New(raw, viewerConfig)
	if err != nil {
		slog.Error("viewer setup", "err", err)
		os.Exit(1)
	}

	root := http.NewServeMux()
	root.Handle("/viewer/", webSrv.Handler())
	root.Handle("/viewer", webSrv.Handler())

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("cask web listening (viewer)", "addr", *bind, "store", *store)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	url := fmt.Sprintf("http://%s/viewer/?token=%s", *bind, token)
	fmt.Fprintf(os.Stderr, "cask web: %s\n", url)
	if !*noOpen {
		openBrowser(url)
	}

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
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
func openBrowser(url string) {
	cmd, args := browserCommand(runtime.GOOS, url)
	if err := exec.Command(cmd, args...).Start(); err != nil {
		slog.Debug("open browser", "err", err) // not fatal
	}
}

// randomToken returns 6 cryptographically random bytes as uppercase
// dash-separated hex groups (viewer-security §5.1: startup token).
func randomToken() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}
