package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestNewRefusesASymlinkedPackFile pins the packfs write path's link refusal
// (cas-core §4.14, go-cask#352): a symbolic link standing at
// <base>/packs/current.pack is not followed. The active pack is opened
// O_CREATE|O_RDWR|O_APPEND and every packed Put appends to it, so following the
// link is a write primitive against the link's target, not merely a
// redirection. Construction fails with the named error and the target's bytes
// are asserted unchanged.
func TestNewRefusesASymlinkedPackFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "store")
	packDir := filepath.Join(base, "packs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "planted-target.pack")
	original := []byte("bytes the pack must not append to")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(packDir, "current.pack")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := New(base, WithEnabled()); !errors.Is(err, fsbackend.ErrUnsafeTarget) {
		t.Fatalf("New over a symlinked current.pack = %v, want ErrUnsafeTarget", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("the link's target changed: %q, want %q", after, original)
	}
}

// TestNewRefusesASymlinkedPackDirectory pins the parent half of the same rule: a
// link where the packs/ directory belongs is refused before os.MkdirAll runs, so
// construction does not even create a directory through it. The link's target is
// asserted to stay empty.
func TestNewRefusesASymlinkedPackDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "packs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := New(base, WithEnabled()); !errors.Is(err, fsbackend.ErrUnsafeTarget) {
		t.Fatalf("New over a symlinked packs directory = %v, want ErrUnsafeTarget", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the packs link's target holds %v (err %v), want it empty", entries, err)
	}
}

// TestNewRefusesASymlinkedLooseTree pins the loose half: packfs.New opens the
// loose tree as a store base of its own, so a link at <base>/loose would redirect
// every object the packed backend mirrors. It is refused like the pack
// directory — a symlinked base is the caller's decision (made once where the
// path is resolved), a symlinked entry inside the base is not.
func TestNewRefusesASymlinkedLooseTree(t *testing.T) {
	base := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "loose")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := New(base, WithEnabled()); !errors.Is(err, fsbackend.ErrUnsafeTarget) {
		t.Fatalf("New over a symlinked loose tree = %v, want ErrUnsafeTarget", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the loose link's target holds %v (err %v), want it empty", entries, err)
	}
}

// TestPutRefusesAPackLinkPlantedAfterNew pins the append path against a link
// that appears after construction: the active handle is released (a second CLI
// invocation over the same base, or a process that came back to a tampered
// store), current.pack is replaced by a link, and the next packed Put must fail
// with the named error rather than append to the target.
func TestPutRefusesAPackLinkPlantedAfterNew(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, sha256.Of([]byte("first")), bytesReader([]byte("first"))); err != nil {
		t.Fatal(err)
	}
	// Release the handle the first Put reopened: the planted link must be seen
	// by the append path, not masked by a descriptor onto the replaced file.
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "planted-target.pack")
	original := []byte("bytes the pack must not append to")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(base, "packs", "current.pack")
	if err := os.Remove(packPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, packPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	payload := []byte("appended after the link was planted")
	err = backend.Put(ctx, sha256.Of(payload), bytesReader(payload))
	if !errors.Is(err, fsbackend.ErrUnsafeTarget) {
		t.Fatalf("Put over a planted pack link = %v, want ErrUnsafeTarget", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("the link's target changed: %q, want %q", after, original)
	}
}

// TestPersistIndexIgnoresAPlantedFixedTempName pins the fixed-name sink the
// index rewrite used to have: the scratch file was "<manifest>.tmp", so a link
// planted at that exact name made every packed Put truncate the link's target.
// The temp is now exclusive and random, so the planted name is inert — the
// write succeeds, the target keeps its bytes, and the planted link survives
// untouched.
func TestPersistIndexIgnoresAPlantedFixedTempName(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	target := filepath.Join(t.TempDir(), "planted-index-target")
	original := []byte("bytes the index rewrite must not truncate")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	planted := backend.manifestPath + ".tmp"
	if err := os.Symlink(target, planted); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	payload := []byte("packed past a planted temp name")
	digest := sha256.Of(payload)
	if err := backend.Put(ctx, digest, bytesReader(payload)); err != nil {
		t.Fatalf("Put with a link at the old fixed temp name = %v, want nil", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("the planted temp link's target changed: %q, want %q", after, original)
	}
	if fi, err := os.Lstat(planted); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the planted link was consumed instead of ignored: (%v, %v)", fi, err)
	}
	data, err := os.ReadFile(backend.manifestPath)
	if err != nil {
		t.Fatalf("read the persisted index: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode the persisted index %q: %v", data, err)
	}
	if _, ok := m.Entries[digest.String()]; !ok {
		t.Fatalf("the persisted index = %v, want the written digest %s", keysOf(m.Entries), digest)
	}
}

// TestPersistIndexCreatesItsTempExclusively pins the mechanism behind the test
// above rather than its symptom: the index scratch file is created through
// os.CreateTemp — an exclusive O_EXCL name built from a random pattern — inside
// the pack directory, and each rewrite gets a name of its own. A fixed name
// would let two writers share one scratch inode (interleaving into a manifest
// that fails to decode at the next New) and would be guessable by an attacker
// planting a link.
func TestPersistIndexCreatesItsTempExclusively(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	var patterns, names []string
	realCreateTemp := backend.op.createTemp
	backend.op.createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := realCreateTemp(dir, pattern)
		if err == nil && strings.HasPrefix(pattern, "index-") {
			patterns = append(patterns, pattern)
			names = append(names, f.Name())
			if filepath.Dir(f.Name()) != backend.packDir {
				t.Errorf("index temp %s is not in the pack directory %s", f.Name(), backend.packDir)
			}
		}
		return f, err
	}

	for _, payload := range [][]byte{[]byte("first packed object"), []byte("second packed object")} {
		if err := backend.Put(ctx, sha256.Of(payload), bytesReader(payload)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	if len(names) != 2 {
		t.Fatalf("the seam saw %d index temps (%v), want one per persistIndex", len(names), names)
	}
	for _, pattern := range patterns {
		if !strings.HasSuffix(pattern, ".tmp") || !strings.Contains(pattern, "*") {
			t.Fatalf("index temp pattern = %q, want a random *.tmp name", pattern)
		}
	}
	if names[0] == names[1] {
		t.Fatalf("both rewrites used the scratch name %q, want a unique name per write", names[0])
	}
	for _, name := range names {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("the scratch file %s was not renamed away: %v", name, err)
		}
	}
}

// TestValidPackRecordRefusesASymlinkedPackParent pins the read half of the rule:
// a record whose pack lives below a symlinked directory under packs/ passes the
// name checks — the record is inside packs/, and the final component is a
// regular file once the link is followed — but the open that serves it would
// read outside the base. The record is invalid, not silently served.
func TestValidPackRecordRefusesASymlinkedPackParent(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	outside := t.TempDir()
	packFile := filepath.Join(outside, "pack.pack")
	if err := os.WriteFile(packFile, bytes.Repeat([]byte("p"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(backend.packDir, "sub")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	digest := sha256.Of([]byte("record behind a symlinked parent"))
	backend.mu.Lock()
	backend.index[string(digest)] = packRecord{Pack: filepath.Join(backend.packDir, "sub", "pack.pack"), Offset: 0, Size: 16}
	backend.mu.Unlock()

	_, err = backend.Get(ctx, digest)
	if !errors.Is(err, errInvalidPackRecord) {
		t.Fatalf("Get behind a symlinked pack parent = %v, want an invalid-record error", err)
	}
	if !errors.Is(err, fsbackend.ErrUnsafeTarget) {
		t.Fatalf("Get behind a symlinked pack parent = %v, want the unsafe-target cause", err)
	}
	// The refusal is an error, not a silent fallback: the same read reports the
	// same cause on the next call, so nothing was dropped and served from the
	// loose tree instead.
	if _, err := backend.Get(ctx, digest); !errors.Is(err, errInvalidPackRecord) {
		t.Fatalf("second Get = %v, want the same refusal", err)
	}
	after, readErr := os.ReadFile(packFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, bytes.Repeat([]byte("p"), 64)) {
		t.Fatalf("the pack behind the link changed: %q", after)
	}
}
