package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestManifestJSONPayloadPinned locks the stored payload shape: a reference is
// one hex digest string, rendered by cas.Digest's text marshaller, with no
// per-type JSON code involved.
func TestManifestJSONPayloadPinned(t *testing.T) {
	h := sha256.Of([]byte("artifact payload"))
	raw, err := json.Marshal(Manifest{Name: "target", Artifacts: []cas.Digest{h}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"target","artifacts":["` + h.String() + `"]}`
	if string(raw) != want {
		t.Fatalf("Manifest JSON = %s, want %s", raw, want)
	}

	// An empty artifact list stays omitted, as before.
	raw, err = json.Marshal(Manifest{Name: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"name":"target"}` {
		t.Fatalf("empty-artifacts Manifest JSON = %s", raw)
	}
}

func newTestApp(t *testing.T) *app {
	t.Helper()
	return newTestAppIn(t, t.TempDir())
}

// newTestAppIn builds the example over a caller-supplied root, for the tests
// that assert on the objects/ + refs/ layout.
func newTestAppIn(t *testing.T, root string) *app {
	t.Helper()
	a, err := newApp(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	return a
}

func writeArtifact(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Acceptance: same bytes → same hash → deduplicated: true.
func TestDedup(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	f := writeArtifact(t, work, "a.bin", "identical bytes")
	h1, dedup1, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if dedup1 {
		t.Fatal("first put must not be deduplicated")
	}
	h2, dedup2, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h2.String() {
		t.Fatalf("identical bytes must hash identically: %s vs %s", h1, h2)
	}
	if !dedup2 {
		t.Fatal("second put of identical bytes must report deduplicated: true")
	}
}

// Acceptance: the second get hits the cache (hit rate > 0).
func TestCacheHitRate(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	f := writeArtifact(t, work, "a.bin", "cache me")
	h, _, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(ctx, h); err != nil {
		t.Fatal(err)
	}
	st := a.cache.CacheStats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("cache stats = %+v, want 1 hit 1 miss", st)
	}
	if st.HitRate <= 0 {
		t.Fatalf("hit rate = %v, want > 0", st.HitRate)
	}
}

// Acceptance: gc deletes only unreferenced artifacts and leaves
// manifest-referenced ones intact.
func TestGC(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	// Two builds of the same target: the manifest ends up referencing only
	// the second artifact; the first becomes garbage.
	f1 := writeArtifact(t, work, "v1.bin", "build v1")
	h1, _, err := a.put(ctx, "app", f1)
	if err != nil {
		t.Fatal(err)
	}
	f2 := writeArtifact(t, work, "v2.bin", "build v2")
	h2, _, err := a.put(ctx, "app", f2)
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() == h2.String() {
		t.Fatal("different content must hash differently")
	}

	n, err := a.gc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("gc deleted %d objects, want >= 1", n)
	}
	if ok, _ := a.backend.Exists(ctx, h1); ok {
		t.Fatal("unreferenced artifact survived gc")
	}
	if ok, _ := a.backend.Exists(ctx, h2); !ok {
		t.Fatal("manifest-referenced artifact was deleted")
	}
}

// A manifest name is a ref: put reads the name's previous ref and moves it to
// the new manifest, and the refs tree stays outside the objects base.
func TestPutMovesNamedRef(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	work := t.TempDir()
	a := newTestAppIn(t, root)

	f1 := writeArtifact(t, work, "v1.bin", "build v1")
	h1, _, err := a.put(ctx, "app", f1)
	if err != nil {
		t.Fatal(err)
	}

	// The name's ref points at the manifest that references the artifact — no
	// store-wide scan is involved.
	mh, err := a.refs.Get(ctx, "app")
	if err != nil {
		t.Fatalf("ref app: %v", err)
	}
	if mh.IsZero() {
		t.Fatal("ref app is absent after put")
	}
	m, err := a.manifests.Get(ctx, mh)
	if err != nil {
		t.Fatalf("ref app does not name a manifest: %v", err)
	}
	if m.Name != "app" || len(m.Artifacts) != 1 || !m.Artifacts[0].Equal(h1) {
		t.Fatalf("manifest behind ref app = %+v, want one artifact %s", m, h1)
	}

	// Layout: the ref is one small file under <root>/refs, beside the objects
	// base — never inside it, where fs.Backend.Clean would reclaim its
	// "<name>.tmp" temp files and List/Stats would report it as an object.
	if _, err := os.Stat(filepath.Join(root, "refs", "app")); err != nil {
		t.Fatalf("ref value file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "objects", "refs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("objects base holds a refs tree (stat err=%v)", err)
	}

	// A second put of different bytes moves the ref and deletes the manifest
	// it replaced, exactly as the scanning version did.
	f2 := writeArtifact(t, work, "v2.bin", "build v2")
	h2, _, err := a.put(ctx, "app", f2)
	if err != nil {
		t.Fatal(err)
	}
	mh2, err := a.refs.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if mh2.Equal(mh) {
		t.Fatal("ref app did not move to the new manifest")
	}
	if ok, _ := a.backend.Exists(ctx, mh); ok {
		t.Fatal("manifest replaced by put survived")
	}
	if ok, _ := a.backend.Exists(ctx, h1); !ok {
		t.Fatal("artifact of the replaced manifest must survive until gc")
	}
	if ok, _ := a.backend.Exists(ctx, h2); !ok {
		t.Fatal("newly stored artifact is missing")
	}
}

// manifestDamage describes one way a stored manifest can rot: it receives the
// object's raw bytes and the envelope decoded from them, and returns the
// damaged bytes to store under a fresh digest.
type manifestDamage struct {
	name   string
	damage func(raw []byte, env cas.Envelope) []byte
}

// damageManifest stores the raw bytes of the manifest at d under the digest of
// their damaged form and returns that digest. The damaged object is a new
// object; the original manifest stays where it is.
func damageManifest(t *testing.T, a *app, d cas.Digest, damage func(raw []byte, env cas.Envelope) []byte) cas.Digest {
	t.Helper()
	ctx := context.Background()
	raw, err := a.manifests.GetRaw(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cas.EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != manifestType {
		t.Fatalf("stored type = %q, want %q", env.Type, manifestType)
	}
	damaged := damage(raw, env)
	cd := sha256.Of(damaged)
	if err := a.backend.Put(ctx, cd, bytes.NewReader(damaged)); err != nil {
		t.Fatal(err)
	}
	return cd
}

// A name whose manifest cannot be read aborts gc instead of being mistaken for
// a leaf: the artifacts that manifest references must survive a sweep that never
// runs. Both damage shapes matter — the second one is why gc asks for the stored
// type instead of reading a failed Store.Get as "not a manifest": Get reports a
// malformed envelope with cas.ErrUnknownType, the very sentinel it uses for a
// stored type that is not manifest@1, so a truncated manifest would otherwise be
// classified as a leaf and its artifacts deleted.
func TestGCAbortsOnCorruptManifest(t *testing.T) {
	damages := []manifestDamage{
		{
			name: "undecodable payload",
			damage: func(raw []byte, env cas.Envelope) []byte {
				// Keep the frame consistent — the declared payload length is
				// still the payload's length — but replace the payload with
				// bytes the gzip codec rejects.
				header := raw[:len(raw)-len(env.Data)]
				payload := bytes.Repeat([]byte{0xff}, len(env.Data))
				return append(append([]byte{}, header...), payload...)
			},
		},
		{
			name:   "truncated envelope",
			damage: func(raw []byte, env cas.Envelope) []byte { return raw[:1] },
		},
	}

	for _, dmg := range damages {
		t.Run(dmg.name, func(t *testing.T) {
			ctx := context.Background()
			work := t.TempDir()
			a := newTestApp(t)

			f := writeArtifact(t, work, "v1.bin", "build v1")
			h1, _, err := a.put(ctx, "app", f)
			if err != nil {
				t.Fatal(err)
			}
			mh, err := a.refs.Get(ctx, "app")
			if err != nil {
				t.Fatal(err)
			}

			// The name's ref now points at the damaged copy of its manifest:
			// the manifest is still there and still names h1, but it no longer
			// reads as one. Treating it as "not a manifest" would delete h1.
			corrupt := damageManifest(t, a, mh, dmg.damage)
			if err := a.refs.Set(ctx, "app", corrupt); err != nil {
				t.Fatal(err)
			}

			before, err := a.backend.List(ctx)
			if err != nil {
				t.Fatal(err)
			}

			n, err := a.gc(ctx)
			if err == nil {
				t.Fatal("gc succeeded over an undecodable manifest; want an error")
			}
			if !errors.Is(err, cas.ErrCorrupt) {
				t.Fatalf("gc error = %v, want it to wrap cas.ErrCorrupt", err)
			}
			if n != 0 {
				t.Fatalf("gc reported %d deleted objects after aborting, want 0", n)
			}
			if ok, err := a.backend.Exists(ctx, h1); err != nil || !ok {
				t.Fatalf("artifact referenced by the corrupt manifest is gone (exists=%v err=%v)", ok, err)
			}
			after, err := a.backend.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("gc removed %d objects before aborting; want none (before=%d after=%d)",
					len(before)-len(after), len(before), len(after))
			}
		})
	}
}

// The count gc reports is the sweep's own deleted-digest count, so it always
// equals the number of objects the store actually lost.
func TestGCReportsSweepCount(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	// Two builds of one target (the first becomes garbage), a second live
	// target, and an artifact no manifest references.
	f1 := writeArtifact(t, work, "v1.bin", "build v1")
	h1, _, err := a.put(ctx, "app", f1)
	if err != nil {
		t.Fatal(err)
	}
	f2 := writeArtifact(t, work, "v2.bin", "build v2")
	h2, _, err := a.put(ctx, "app", f2)
	if err != nil {
		t.Fatal(err)
	}
	f3 := writeArtifact(t, work, "lib.bin", "build lib")
	h3, _, err := a.put(ctx, "lib", f3)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := a.artifacts.Put(ctx, &Artifact{Name: "orphan", Data: []byte("never referenced")})
	if err != nil {
		t.Fatal(err)
	}

	before, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.gc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The reported count IS the number of objects that disappeared.
	if n != len(before)-len(after) {
		t.Fatalf("gc reported %d deletions but removed %d objects", n, len(before)-len(after))
	}
	if n != 2 {
		t.Fatalf("gc deleted %d objects, want 2 (the replaced artifact %s and the orphan %s)", n, h1, orphan)
	}
	gone := make(map[string]bool, len(before))
	for _, d := range before {
		gone[d.String()] = true
	}
	for _, d := range after {
		delete(gone, d.String())
	}
	if len(gone) != n {
		t.Fatalf("%d digests disappeared but gc reported %d: %v", len(gone), n, gone)
	}
	for _, d := range []cas.Digest{h2, h3} {
		if ok, _ := a.backend.Exists(ctx, d); !ok {
			t.Fatalf("manifest-referenced object %s was deleted", d)
		}
	}
	for _, d := range []cas.Digest{h1, orphan} {
		if ok, _ := a.backend.Exists(ctx, d); ok {
			t.Fatalf("unreachable object %s survived gc", d)
		}
	}
}

// A get of a missing hash fails with ErrNotFound.
func TestGetMissing(t *testing.T) {
	a := newTestApp(t)
	missing := sha256.Of([]byte("never stored"))
	if _, err := a.get(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("get(missing) = %v, want ErrNotFound", err)
	}
}

func TestRunCommands(t *testing.T) {
	store := t.TempDir()
	work := t.TempDir()
	f := filepath.Join(work, "a.bin")
	if err := os.WriteFile(f, []byte("hello artifacts"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// put
	if code := run([]string{"-store", store, "put", "v1", f}, &stdout, &stderr); code != 0 {
		t.Fatalf("put code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deduplicated: false") {
		t.Fatalf("put out=%q", stdout.String())
	}
	hash, _, _ := strings.Cut(stdout.String(), " ")
	stdout.Reset()
	stderr.Reset()

	// get by hash
	if code := run([]string{"-store", store, "get", hash}, &stdout, &stderr); code != 0 {
		t.Fatalf("get code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "15 bytes") {
		t.Fatalf("get out=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()

	// get by ref name: the same artifact, resolved through the name's manifest
	// ref instead of a store-wide scan.
	if code := run([]string{"-store", store, "get", "v1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("get by name code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "v1 (15 bytes)") {
		t.Fatalf("get by name out=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()

	// get with an argument that is neither a stored ref nor a digest: an
	// unknown name, an empty one, a name refs.ValidateName rejects, and a
	// name-shaped path that has no ref file.
	for _, arg := range []string{"not-a-ref", "", "../escape", "no/such/ref"} {
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{"-store", store, "get", arg}, &stdout, &stderr); code != 1 {
			t.Fatalf("get %q code=%d, want 1 (stderr=%s)", arg, code, stderr.String())
		}
	}
	stdout.Reset()
	stderr.Reset()

	// stats
	if code := run([]string{"-store", store, "stats"}, &stdout, &stderr); code != 0 {
		t.Fatalf("stats code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()

	// gc
	if code := run([]string{"-store", store, "gc"}, &stdout, &stderr); code != 0 {
		t.Fatalf("gc code=%d", code)
	}
}

// TestRunUsageErrors pins the usage errors. The cases that get past argument
// checking are given an explicit -store, so the run never creates the default
// ./store tree inside the package directory.
func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dir := t.TempDir()
	cases := [][]string{
		{},                         // no args
		{"-store"},                 // -store missing value
		{"-store", dir},            // -store with no command (must not panic)
		{"-store", dir, "put"},     // put missing args
		{"-store", dir, "get"},     // get missing hash
		{"-store", dir, "unknown"}, // unknown command
	}
	for _, c := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := run(c, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", c, code)
		}
	}
}

// TestRunStoreFlagForms pins the standard flag parsing: the store directory may
// be spelled -store dir, -store=dir, or --store dir, and -h/--help and an
// unknown flag are usage errors that print the usage text (exit 2).
func TestRunStoreFlagForms(t *testing.T) {
	store := t.TempDir()
	var stdout, stderr bytes.Buffer

	for _, args := range [][]string{
		{"-store", store, "stats"},
		{"-store=" + store, "stats"},
		{"--store", store, "stats"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("args %v: code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "objects") {
			t.Fatalf("args %v: stats output = %q", args, stdout.String())
		}
	}

	for _, args := range [][]string{
		{"-h"},
		{"--help"},
		{"-store=" + store, "-nope", "stats"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage: artifacts") {
			t.Fatalf("args %v: stderr = %q, want the usage text", args, stderr.String())
		}
	}
}
