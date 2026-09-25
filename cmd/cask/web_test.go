package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/store"
)

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
