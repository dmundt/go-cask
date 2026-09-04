package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/internal/web"
)

// runWeb starts the embedded viewer — the product's only HTTP surface
// (backend-architecture §3). Invoking `cask web` IS the explicit enablement
// (viewer-security §3); binding to a non-loopback address requires explicit
// confirmation (viewer-security §4).
func runWeb(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	store := fs.String("store", "./objects", "filesystem store directory")
	bind := fs.String("bind", "127.0.0.1:8080", "listen address")
	tokens := fs.String("tokens", "", "comma-separated role=token pairs for viewer login (e.g. admin=sekret,operator=op)")
	allowInsecure := fs.Bool("allow-insecure-bind", false, "allow a non-loopback bind without HTTPS")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !isLoopbackBind(*bind) && !*allowInsecure {
		slog.Error("refusing to bind the viewer to a non-loopback address without HTTPS; set -allow-insecure-bind to override")
		os.Exit(1)
	}

	raw, err := cas.NewFSRawStore(*store)
	if err != nil {
		slog.Error("open store", "err", err)
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
		if ok {
			roleTokens[strings.TrimSpace(tok)] = strings.TrimSpace(role)
		}
	}

	token := randomToken()
	slog.Warn("viewer startup token", "admin_token", token) // printed once, never stored
	webSrv, err := web.New(raw, web.Config{
		StartupToken: token,
		RoleTokens:   roleTokens,
		Secure:       false, // loopback default; set with TLS
	})
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

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
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
