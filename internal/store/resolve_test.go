package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// resolvedThrough is filepath.EvalSymlinks with the test's error handling, so an
// expectation is written the way the platform spells the same directory.
func resolvedThrough(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

// mustSymlink creates a symbolic link, skipping the test where the platform
// cannot (Windows without the symlink privilege).
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// TestResolveBaseFollowsLinksAndKeepsMissingTails pins the one resolution rule
// the CLI applies at its store-opening seam (cli.md §2, go-cask#353): every
// symbolic link in the path is followed, so a sweep acts on the directory the
// operator is really pointing at, while a tail that does not exist yet is kept
// verbatim because a path that is not there cannot be a link (the CLI creates a
// store directory on first use). An empty base is the caller's to reject, and a
// path that cannot be resolved at all is an error rather than a half-resolved
// base.
func TestResolveBaseFollowsLinksAndKeepsMissingTails(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-store")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	realResolved := resolvedThrough(t, real)

	t.Run("a real directory resolves to itself", func(t *testing.T) {
		got, err := ResolveBase(real)
		if err != nil || got != realResolved {
			t.Fatalf("ResolveBase(%q) = (%q, %v), want %q", real, got, err, realResolved)
		}
	})

	t.Run("a missing path is kept as named", func(t *testing.T) {
		absent := filepath.Join(real, "absent")
		got, err := ResolveBase(absent)
		if err != nil || got != filepath.Join(realResolved, "absent") {
			t.Fatalf("ResolveBase(%q) = (%q, %v), want the path unchanged", absent, got, err)
		}
	})

	t.Run("a symlinked base resolves to its target", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "store")
		mustSymlink(t, real, link)
		got, err := ResolveBase(link)
		if err != nil || got != realResolved {
			t.Fatalf("ResolveBase(%q) = (%q, %v), want %q", link, got, err, realResolved)
		}
	})

	t.Run("a missing tail below a symlinked parent resolves the parent", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "store")
		mustSymlink(t, real, link)
		got, err := ResolveBase(filepath.Join(link, "created-on-first-use"))
		if err != nil || got != filepath.Join(realResolved, "created-on-first-use") {
			t.Fatalf("ResolveBase(link+tail) = (%q, %v), want the target's child", got, err)
		}
	})

	t.Run("a relative base keeps its relative form", func(t *testing.T) {
		t.Chdir(root)
		got, err := ResolveBase("absent-store")
		if err != nil || got != "absent-store" {
			t.Fatalf("ResolveBase(relative) = (%q, %v), want the relative path", got, err)
		}
	})

	t.Run("an empty base is the caller's to reject", func(t *testing.T) {
		for _, base := range []string{"", "   "} {
			got, err := ResolveBase(base)
			if err != nil || got != base {
				t.Fatalf("ResolveBase(%q) = (%q, %v), want it unchanged", base, got, err)
			}
		}
	})

	t.Run("an unresolvable base is an error", func(t *testing.T) {
		file := filepath.Join(root, "not-a-directory")
		if err := os.WriteFile(file, []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveBase(filepath.Join(file, "store")); err == nil {
			t.Fatal("ResolveBase below a regular file = nil, want an error")
		}
	})
}

// TestOpenResolvesASymlinkedStoreBase pins the seam: the store is opened over the
// resolved directory, Store.Path reports it, and the objects really land there —
// so a symlinked -store is followed deliberately and visibly instead of being
// silently read and swept through the link (cli.md §2, go-cask#353).
func TestOpenResolvesASymlinkedStoreBase(t *testing.T) {
	ctx := context.Background()
	real := filepath.Join(t.TempDir(), "real-store")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	realResolved := resolvedThrough(t, real)
	link := filepath.Join(t.TempDir(), "store")
	mustSymlink(t, real, link)

	for _, kind := range selectableKinds {
		t.Run(string(kind), func(t *testing.T) {
			st, err := Open(ctx, Options{Kind: kind, Path: link})
			if err != nil {
				t.Fatalf("Open(%q) over a symlinked base: %v", kind, err)
			}
			defer st.Close()
			if got := st.Path(); got != realResolved {
				t.Fatalf("Store.Path() = %q, want the resolved base %q", got, realResolved)
			}
			payload := []byte("bytes written through a symlinked -store")
			digest := sha256.Of(payload)
			if err := st.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			// The object is stored beneath the resolved base — the tree the
			// operator's link names — and comes back through the same store.
			if _, err := os.Stat(filepath.Join(realResolved, digest.String()[:2], digest.String())); err != nil {
				t.Fatalf("the object is not beneath the resolved base: %v", err)
			}
			reader, err := st.Get(ctx, digest)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("viewer", func(t *testing.T) {
		backend, err := OpenViewer(ctx, Options{Kind: KindFS, Path: link})
		if err != nil {
			t.Fatalf("OpenViewer over a symlinked base: %v", err)
		}
		if got := backend.BasePath(); got != realResolved {
			t.Fatalf("OpenViewer BasePath() = %q, want the resolved base %q", got, realResolved)
		}
	})
}

// TestStorePathFallsBackToABackendBuiltByHand pins the accessor's contract for a
// Store assembled without the constructor — a test, a decorator: it reports the
// directory the backend's bytes live under rather than an empty string, so a
// command that names the store it acted on still names something real.
func TestStorePathFallsBackToABackendBuiltByHand(t *testing.T) {
	base := t.TempDir()
	opened, err := Open(context.Background(), Options{Kind: KindFS, Path: base})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	hand := &Store{Backend: opened.Backend, Capabilities: opened.Capabilities, Kind: KindFS}
	if got := hand.Path(); got != resolvedThrough(t, base) {
		t.Fatalf("hand-built Store.Path() = %q, want the backend's base %q", got, resolvedThrough(t, base))
	}
	var nilStore *Store
	if got := nilStore.Path(); got != "" {
		t.Fatalf("nil Store.Path() = %q, want an empty path", got)
	}
}
