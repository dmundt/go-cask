// Command api-server is the HTTP-exposure pattern server (examples/api):
// it serves the versioned /api/cas/v1 surface over the public cas
// library, with bearer-token role auth and IP-based rate limiting.
//
// It ships no credential of its own: -tokens is required, and the server
// refuses to start without it (api-design §7, viewer-security §11).
//
// Usage:
//
//	go run ./examples/api/server -store ./objects \
//	    -tokens "viewer=v_tok,operator=o_tok,admin=a_tok" -bind 127.0.0.1:8080
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

// Connection lifetimes of the example server: a client cannot trickle a body
// forever, and a slow reader of a streamed object cannot pin its goroutine and
// object file forever. writeTimeout is generous because it also bounds a large
// download.
const (
	readTimeout  = 60 * time.Second
	writeTimeout = 5 * time.Minute
	idleTimeout  = 2 * time.Minute
)

// newHTTPServer builds the example server with every connection lifetime set
// (api-design §9). Kept separate from main so a test can pin them.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// stringList collects a repeatable flag: one entry per occurrence.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func main() {
	var (
		store         = flag.String("store", "./objects", "filesystem store directory")
		bind          = flag.String("bind", "127.0.0.1:8080", "listen address")
		tokens        = flag.String("tokens", "", `comma-separated role=token pairs (required; no default credential ships)`)
		trustedProxy  stringList
		burst         = flag.Int("burst", 20, "rate-limit burst per IP")
		rate          = flag.Float64("rate", 2, "rate-limit sustained requests/sec per IP")
		maxObjectSize = flag.Int64("max-size", DefaultMaxObjectBytes, "maximum object body in bytes (oversized uploads are refused with 413)")
	)
	flag.Var(&trustedProxy, "trusted-proxy", "host whose X-Forwarded-For header names the caller (repeatable; enables per-caller rate limiting behind a proxy)")
	flag.Parse()

	tokenMap, err := validateTokens(*tokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "api-server: -tokens: %v\n", err)
		os.Exit(2)
	}
	if *maxObjectSize <= 0 {
		fmt.Fprintf(os.Stderr, "api-server: -max-size: must be positive, got %d\n", *maxObjectSize)
		os.Exit(2)
	}

	backend, err := fs.New(*store)
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	cfg := DefaultRateLimit()
	cfg.Burst = *burst
	cfg.RequestsPerSecond = *rate
	srv := New(backend, tokenMap, cfg, *maxObjectSize).WithTrustedProxies(trustedProxy...)

	httpSrv := newHTTPServer(*bind, srv.Handler())
	go func() {
		slog.Info("api listening", "addr", *bind, "store", *store,
			"trusted_proxies", trustedProxy, "max_size", *maxObjectSize)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
