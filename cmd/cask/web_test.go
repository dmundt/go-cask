package main

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/store"
)

// TestViewerServerTimeouts pins the viewer's connection bounds
// (viewer-security §13, defaults §4): only the header phase was bounded before,
// so a client that completed it could dribble or stall a request body forever,
// holding the connection and its goroutine. Every deadline is set, and the
// whole-request read covers the header phase it must contain.
func TestViewerServerTimeouts(t *testing.T) {
	srv := viewerServer(http.NotFoundHandler())
	for _, tc := range []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"WriteTimeout", srv.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	} {
		if tc.got <= 0 {
			t.Errorf("%s = %v, want a non-zero deadline", tc.name, tc.got)
		}
	}
	if srv.ReadTimeout < srv.ReadHeaderTimeout {
		t.Errorf("ReadTimeout %v is shorter than the ReadHeaderTimeout %v it contains",
			srv.ReadTimeout, srv.ReadHeaderTimeout)
	}
	// The write deadline also covers handler execution, and one route sweeps the
	// whole store before it writes anything, so it must leave that room.
	if srv.WriteTimeout < srv.ReadTimeout {
		t.Errorf("WriteTimeout %v is shorter than ReadTimeout %v", srv.WriteTimeout, srv.ReadTimeout)
	}
}

// TestRunWebRefusesPackedStore pins the operator-facing refusal at the CLI
// boundary: a packed store is a legitimate choice for every other subcommand,
// so `web` must fail (exit 1) instead of starting a viewer that cannot read it.
// The message itself — operation, backend and remedy — is pinned in
// internal/store (cli.md §1, §2, viewer-design §1).
func TestRunWebRefusesPackedStore(t *testing.T) {
	code := runWeb(context.Background(), modeFlags{store: t.TempDir()},
		[]string{"-backend", string(store.KindPackFS), "-bind", "127.0.0.1:0", "-no-open"})
	if code != 1 {
		t.Fatalf("runWeb(-backend packfs) exit = %d, want 1", code)
	}
}

// TestSplitList pins the -trusted-proxy value grammar: a comma-separated
// list whose entries are trimmed and whose empty entries are dropped, so
// the flag can be written with the spacing a command line invites.
func TestSplitList(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string
	}{
		{"", nil},
		{"   ", nil},
		{"10.0.0.0/8", []string{"10.0.0.0/8"}},
		{" 10.0.0.0/8 , 127.0.0.1 ", []string{"10.0.0.0/8", "127.0.0.1"}},
		{"10.0.0.0/8,,127.0.0.1,", []string{"10.0.0.0/8", "127.0.0.1"}},
	} {
		if got := splitList(tc.value); !slices.Equal(got, tc.want) {
			t.Errorf("splitList(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestRunWebTrustedProxy pins the startup behavior of -trusted-proxy: a valid
// list starts the viewer (viewer-security §5.2), and a malformed entry fails
// startup instead of quietly trusting nothing.
func TestRunWebTrustedProxy(t *testing.T) {
	runWebOnce := func(args ...string) int {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-time.After(150 * time.Millisecond)
			cancel()
		}()
		defer cancel()
		return runWeb(ctx, modeFlags{store: t.TempDir()}, args)
	}

	if code := runWebOnce("-bind", "127.0.0.1:0", "-no-open", "-trusted-proxy", "10.0.0.0/8, 127.0.0.1"); code != 0 {
		t.Fatalf("runWeb with a valid -trusted-proxy exit = %d, want 0", code)
	}
	if code := runWebOnce("-bind", "127.0.0.1:0", "-no-open", "-trusted-proxy", "not-an-address"); code != 1 {
		t.Fatalf("runWeb with a malformed -trusted-proxy exit = %d, want 1", code)
	}
}
