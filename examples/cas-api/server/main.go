// Command cas-api-server is a standalone CAS HTTP API server
// (examples/cas-api): it serves /api/cas/v1 per cas-api.instructions.md
// with bearer-token role auth and IP-based rate limiting.
//
// Usage:
//
//	go run ./examples/cas-api/server -store ./objects \
//	    -tokens "viewer=v_tok,operator=o_tok,admin=a_tok" -bind 127.0.0.1:8080
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

	"github.com/dmundt/go-cask/cas"
)

func main() {
	var (
		store  = flag.String("store", "./objects", "filesystem store directory")
		bind   = flag.String("bind", "127.0.0.1:8080", "listen address")
		tokens = flag.String("tokens", "viewer=viewer,operator=operator,admin=admin", "comma-separated role=token pairs")
		burst  = flag.Int("burst", 20, "rate-limit burst per IP")
		rate   = flag.Float64("rate", 2, "rate-limit sustained requests/sec per IP")
	)
	flag.Parse()

	raw, err := cas.NewFSRawStore(*store)
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
	cfg := DefaultRateLimit()
	cfg.Burst = *burst
	cfg.RequestsPerSecond = *rate
	srv := New(raw, tokenMap, cfg)

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("cas-api listening", "addr", *bind, "store", *store)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}
