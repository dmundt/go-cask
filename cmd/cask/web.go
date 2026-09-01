package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmundt/go-cask/internal/api"
	"github.com/dmundt/go-cask/internal/auth"
	"github.com/dmundt/go-cask/internal/storage"
)

// runWeb starts the embedded server: CAS API + OpenAPI (and the viewer when
// it lands in internal/web) over the store, with bearer-role auth and
// IP-based rate limiting. Graceful shutdown on SIGINT/SIGTERM.
func runWeb(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	store := fs.String("store", "./objects", "filesystem store directory")
	bind := fs.String("bind", "127.0.0.1:8080", "listen address")
	tokens := fs.String("tokens", "viewer=viewer,operator=operator,admin=admin", "comma-separated role=token pairs")
	rate := fs.Float64("rate", 2, "rate-limit sustained requests/sec per IP")
	burst := fs.Int("burst", 20, "rate-limit burst per IP")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	srv := api.New(st, tokenMap, rlCfg)
	defer srv.Close()

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("cask web listening", "addr", *bind, "store", *store)
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
