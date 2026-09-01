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

	"github.com/dmundt/go-cask/internal/api"
	"github.com/dmundt/go-cask/internal/auth"
	"github.com/dmundt/go-cask/internal/storage"
	"github.com/dmundt/go-cask/internal/web"
)

// runWeb starts the embedded server: the CAS API and, when enabled, the
// viewer — one server, one mux (backend-architecture §3). The viewer is
// disabled by default (viewer-security §3: secure by default); enabling it
// generates a startup admin token printed once, and binding to a
// non-loopback address requires explicit confirmation (viewer-security §4).
func runWeb(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	store := fs.String("store", "./objects", "filesystem store directory")
	bind := fs.String("bind", "127.0.0.1:8080", "listen address")
	tokens := fs.String("tokens", "viewer=viewer,operator=operator,admin=admin", "comma-separated role=token pairs")
	rate := fs.Float64("rate", 2, "rate-limit sustained requests/sec per IP")
	burst := fs.Int("burst", 20, "rate-limit burst per IP")
	viewer := fs.Bool("viewer", false, "enable the embedded technical viewer (secure by default)")
	allowInsecure := fs.Bool("allow-insecure-bind", false, "allow a non-loopback bind for the viewer without HTTPS")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *viewer && !isLoopbackBind(*bind) && !*allowInsecure {
		slog.Error("refusing to bind the viewer to a non-loopback address without HTTPS; set -allow-insecure-bind to override")
		os.Exit(1)
	}

	st, err := storage.New(ctx, storage.Config{Dir: *store})
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	tokenMap := map[string]string{}
	for _, pair := range strings.Split(*tokens, ",") {
		role, tok, ok := strings.Cut(pair, "=")
		if ok {
			tokenMap[strings.TrimSpace(tok)] = strings.TrimSpace(role)
		}
	}
	rlCfg := auth.DefaultRateLimit()
	rlCfg.RequestsPerSecond = *rate
	rlCfg.Burst = *burst
	apiSrv := api.New(st, tokenMap, rlCfg)
	defer apiSrv.Close()

	root := http.NewServeMux()
	root.Handle("/api/cas/v1/", apiSrv.Handler())

	if *viewer {
		token := randomToken()
		slog.Warn("viewer enabled", "admin_token", token) // printed once, never stored
		webSrv, err := web.New(st, web.Config{
			Enabled:      true,
			StartupToken: token,
			RoleTokens:   tokenMap,
			Secure:       false, // loopback default; set with TLS
		})
		if err != nil {
			slog.Error("viewer setup", "err", err)
			os.Exit(1)
		}
		root.Handle("/viewer/", webSrv.Handler())
		root.Handle("/viewer", webSrv.Handler())
	}

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("cask web listening", "addr", *bind, "store", *store, "viewer", *viewer)
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
