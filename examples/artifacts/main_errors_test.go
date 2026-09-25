package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
)

// This file covers the artifacts CLI's error and command-dispatch paths that
// the existing acceptance tests do not reach, plus the two pieces the example
// deliberately keeps apart from main: the manifest model's empty case and the
// cache monitor's periodic snapshot.
//
// Deliberately left uncovered, with the reason:
//
//   - newApp's lru.New error return (main.go:138) and its monitor-snapshot
//     return (main.go:142) need a cache constructor that rejects a valid
//     capacity and a printer that fails; the example passes the literal 100 and
//     fmt.Printf.
//   - put's artifact-put, manifest-put and replaced-manifest-delete returns
//     (main.go:168, 172, 183) and artifactFor's store-failure branch
//     (main.go:216): each needs the fs backend to fail mid-operation, and the
//     example takes a concrete *fs.Backend with no injectable seam — the real
//     filesystem does not fail on a valid t.TempDir.
//   - gc's refs-read and sweep returns (main.go:251, 273) for the same reason;
//     the mark-phase abort is covered below with a real damaged object.
//   - run's get and monitor runtime-error returns (main.go:335, 342): both wrap
//     the app-level failures above (a corrupt ref, a failing store).
//   - run's stats error return (main.go:349): same, the backend is concrete.
//   - main (main.go:375): the process entry point, which exists only to call
//     run with os.Args and os.Exit.

// A name whose manifest no longer resolves as a manifest is a get error, never
// a silent empty artifact.
func TestArtifactForDanglingManifest(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	missing := sha256.Of([]byte("a manifest that was never stored"))
	if err := a.refs.Set(ctx, "dangling", missing); err != nil {
		t.Fatal(err)
	}
	d, err := a.artifactFor(ctx, "dangling")
	if err == nil {
		t.Fatalf("artifactFor(dangling ref) = %s, want an error", d)
	}
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("artifactFor(dangling ref) = %v, want it to wrap ErrNotFound", err)
	}
}

// A manifest that names zero or more than one artifact cannot answer "which
// artifact is this ref?", so artifactFor refuses it instead of guessing.
func TestArtifactForRequiresExactlyOneArtifact(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		artifacts []cas.Digest
	}{
		{name: "empty manifest", artifacts: nil},
		{
			name: "manifest with two artifacts",
			artifacts: []cas.Digest{
				sha256.Of([]byte("first")),
				sha256.Of([]byte("second")),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			mh, _, err := a.manifests.PutDedup(ctx, &Manifest{Name: "multi", Artifacts: tc.artifacts})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.refs.Set(ctx, "multi", mh); err != nil {
				t.Fatal(err)
			}
			if _, err := a.artifactFor(ctx, "multi"); err == nil {
				t.Fatal("artifactFor must refuse a manifest that does not name exactly one artifact")
			}
		})
	}
}

// An empty manifest references nothing, so gc treats it as a leaf rather than
// walking a nil slice.
func TestManifestReferencesEmpty(t *testing.T) {
	m := &Manifest{Name: "empty"}
	if refs := m.References(); refs != nil {
		t.Fatalf("empty Manifest.References() = %v, want nil", refs)
	}
}

// An Artifact is a leaf: it references nothing, so a walk stops there.
func TestArtifactReferencesAreNil(t *testing.T) {
	a := &Artifact{Name: "leaf", Data: []byte("bytes")}
	if refs := a.References(); refs != nil {
		t.Fatalf("Artifact.References() = %v, want nil", refs)
	}
}

// A root the manifest store cannot even read a header from aborts gc before it
// sweeps anything: Store.Type's ErrCorrupt is not "a leaf".
func TestGCAbortsOnRawRoot(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	// A stored artifact the ref will point at, and a raw (un-enveloped) blob
	// standing in for a corrupt root.
	keep, err := a.artifacts.Put(ctx, &Artifact{Name: "keep", Data: []byte("keep me")})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("not an envelope at all")
	rawDigest := sha256.Of(raw)
	if err := a.backend.Put(ctx, rawDigest, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if err := a.refs.Set(ctx, "broken", rawDigest); err != nil {
		t.Fatal(err)
	}

	before, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.gc(ctx)
	if err == nil {
		t.Fatal("gc over a root with an unreadable header must fail")
	}
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("gc error = %v, want it to wrap cas.ErrCorrupt", err)
	}
	if n != 0 {
		t.Fatalf("gc reported %d deletions after aborting, want 0", n)
	}
	after, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("gc removed %d objects before aborting, want 0", len(before)-len(after))
	}
	if ok, _ := a.backend.Exists(ctx, keep); !ok {
		t.Fatal("the artifact reachable from the readable ref was swept")
	}
}

// newApp reports each unusable root: a regular file where <root>/objects would
// go and a regular file where <root>/refs would go.
func TestNewAppRejectsUnusableRoot(t *testing.T) {
	cases := []struct {
		name  string
		block string // the path under root that is a regular file, not a directory
	}{
		{name: "objects base cannot be created", block: "objects"},
		{name: "refs directory cannot be created", block: "refs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.block), []byte("in the way"), 0o644); err != nil {
				t.Fatal(err)
			}
			a, err := newApp(root)
			if err == nil {
				a.close()
				t.Fatalf("newApp with %s as a file returned no error", tc.block)
			}
		})
	}
}

// put refuses a file it cannot read and stores nothing under the name.
func TestPutUnreadableFile(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	if _, _, err := a.put(ctx, "target", filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("put of a missing file must fail")
	}
	if _, err := a.refs.Get(ctx, "target"); !errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("ref target after a failed put = %v, want ErrNotFound", err)
	}
}

// corruptRef overwrites a ref's value file with text that is not a digest, so
// the next read of that name fails instead of reporting it absent.
func corruptRef(t *testing.T, root, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "refs", name), []byte("not a digest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// put reads the name's previous ref before writing anything, so an unreadable
// ref aborts the put with no object stored.
func TestPutUnreadableRef(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	work := t.TempDir()
	a := newTestAppIn(t, root)
	corruptRef(t, root, "target")

	before, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := writeArtifact(t, work, "a.bin", "payload")
	if _, _, err := a.put(ctx, "target", f); err == nil {
		t.Fatal("put must fail when the name's ref cannot be read")
	}
	after, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("put stored %d objects before failing on the ref, want 0", len(after)-len(before))
	}
}

// artifactFor surfaces an unreadable ref as a read failure, distinct from
// "there is no such ref, so try a digest".
func TestArtifactForUnreadableRef(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a := newTestAppIn(t, root)
	corruptRef(t, root, "target")

	if _, err := a.artifactFor(ctx, "target"); err == nil {
		t.Fatal("artifactFor must fail on an unreadable ref")
	} else if errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("artifactFor(unreadable ref) = %v, want a read error, not ErrNotFound", err)
	}
}

// A store root that cannot hold the layout is a runtime error (exit 1).
func TestRunStoreError(t *testing.T) {
	notADir := writeArtifact(t, t.TempDir(), "not-a-dir", "x")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-store", notADir, "stats"}, &stdout, &stderr); code != 1 {
		t.Fatalf("stats with an unusable store root: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
}

// The monitor command reports a malformed hash and a missing object as runtime
// errors (exit 1) instead of counting a phantom cache access.
func TestRunMonitorErrors(t *testing.T) {
	store := t.TempDir()
	var stdout, stderr bytes.Buffer

	cases := []struct {
		name string
		args []string
	}{
		{name: "malformed hash", args: []string{"-store", store, "monitor", "not-a-hash"}},
		{name: "hash that was never stored", args: []string{"-store", store, "monitor", sha256.Of([]byte("absent")).String()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			if code := run(tc.args, &stdout, &stderr); code != 1 {
				t.Fatalf("monitor %v: code=%d, want 1 (stderr=%q)", tc.args[3:], code, stderr.String())
			}
		})
	}
}

// The monitor command warms the cache and prints the final counters.
func TestRunMonitorSuccess(t *testing.T) {
	ctx := context.Background()
	store := t.TempDir()
	work := t.TempDir()
	a := newTestAppIn(t, store)
	f := writeArtifact(t, work, "m.bin", "monitor me")
	h, _, err := a.put(ctx, "m", f)
	if err != nil {
		t.Fatal(err)
	}
	a.close()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-store", store, "monitor", h.String()}, &stdout, &stderr); code != 0 {
		t.Fatalf("monitor code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "final: hits=") {
		t.Fatalf("monitor output = %q, want the final cache counters", stdout.String())
	}
}

// put of an unreadable file through the CLI is a runtime error (exit 1), not a
// usage error.
func TestRunPutUnreadableFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-store", t.TempDir(), "put", "name", filepath.Join(t.TempDir(), "missing.bin")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("put of a missing file: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
}

// The monitor's goroutine emits a snapshot on its interval, starting at
// construction, until Stop waits for it to exit.
func TestCacheMonitorEmitsSnapshots(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	defer a.close()

	// A monitor of this test's own, with an interval short enough to observe
	// without slowing the suite.
	snapshots := make(chan CacheSnapshot, 4)
	m := NewCacheMonitor(a.cache.CachedStore(), time.Millisecond, func(s CacheSnapshot) {
		select {
		case snapshots <- s:
		default: // the channel is a probe, never a brake on the monitor
		}
	})
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			m.Stop()
		}
	})

	if _, err := a.artifacts.Put(ctx, &Artifact{Name: "monitored", Data: []byte("payload")}); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-snapshots:
		if s.Size < 0 {
			t.Fatalf("snapshot size = %d, want >= 0", s.Size)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the monitor emitted no snapshot within 2s")
	}

	m.Stop()
	stopped = true
	// Stop is idempotent: a second call returns immediately instead of
	// blocking on an already-closed channel.
	m.Stop()
}

// A monitor that was never started (the zero value) has nothing to stop, so
// Stop must be safe on it.
func TestCacheMonitorStopWithoutStart(t *testing.T) {
	var m CacheMonitor[*Artifact]
	m.Stop() // must not panic or block
}
