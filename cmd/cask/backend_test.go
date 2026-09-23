package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	memory "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/internal/store"
)

// packFSMode is a modeFlags selecting the pack backend over a fresh directory.
func packFSMode(t *testing.T) modeFlags {
	t.Helper()
	return modeFlags{store: t.TempDir(), backend: string(store.KindPackFS)}
}

// putFile stores content through the CLI and returns the printable digest.
func putFile(t *testing.T, mf modeFlags, content string) string {
	t.Helper()
	out, code := run(t, mf, "put", writeTemp(t, content))
	if code != 0 {
		t.Fatalf("put %q exit %d", content, code)
	}
	return strings.TrimSpace(out)
}

// requirePackIndex asserts the pack index is on disk and names the digest.
func requirePackIndex(t *testing.T, storeDir, printable string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeDir, "packs", "index.json"))
	if err != nil {
		t.Fatalf("read pack index: %v", err)
	}
	hexDigest := strings.TrimPrefix(printable, "sha256:")
	if !strings.Contains(string(data), hexDigest) {
		t.Fatalf("pack index %s does not name %s", data, printable)
	}
}

// TestBackendFlagForms covers the global -backend flag forms and the usage
// error for a value no backend answers to (cli.md §1, §3, §4).
func TestBackendFlagForms(t *testing.T) {
	for _, args := range [][]string{
		{"-backend", "packfs", "list"},
		{"-backend=packfs", "list"},
		{"--backend=packfs", "list"},
	} {
		mf, cmd, _, err := parseGlobal(args)
		if err != nil || cmd != "list" || mf.backend != "packfs" {
			t.Fatalf("parseGlobal(%v) = (%+v, %q, %v), want backend packfs and command list", args, mf, cmd, err)
		}
	}
	// An absent -backend keeps its zero value, which selects the documented
	// default (fs) without spelling it in the parser.
	mf, _, _, err := parseGlobal([]string{"put", "file.bin"})
	if err != nil || mf.backend != "" {
		t.Fatalf("parseGlobal without -backend = (%+v, %v), want an unset backend", mf, err)
	}
	if kind, err := store.ParseKind(mf.backend); err != nil || kind != store.KindFS {
		t.Fatalf("ParseKind(%q) = (%q, %v), want the fs default", mf.backend, kind, err)
	}

	if _, code := run(t, modeFlags{store: t.TempDir(), backend: "sqlite"}, "list"); code != 2 {
		t.Fatalf("list -backend sqlite exit = %d, want 2 (usage)", code)
	}
}

// TestPackFSStoreOperations: every store subcommand runs against a packed
// store, and the write path leaves the pack index on disk — the next CLI
// invocation reopens it and must see exactly the objects that were written.
func TestPackFSStoreOperations(t *testing.T) {
	mf := packFSMode(t)
	h1 := putFile(t, mf, "one")
	h2 := putFile(t, mf, "two")
	requirePackIndex(t, mf.store, h1)

	// get round-trips through the reopened pack index.
	out, code := run(t, mf, "get", h1)
	if code != 0 || out != "one" {
		t.Fatalf("get = (%q, %d), want one", out, code)
	}

	// list and list -json report both objects, with no phantom digests from a
	// mangled index key.
	out, code = run(t, mf, "list", "-json")
	if code != 0 {
		t.Fatalf("list -json exit %d", code)
	}
	var listOut struct {
		Total   int `json:"total"`
		Objects []struct {
			Hash string `json:"hash"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(out), &listOut); err != nil {
		t.Fatalf("list -json output %q: %v", out, err)
	}
	if listOut.Total != 2 || len(listOut.Objects) != 2 {
		t.Fatalf("list -json = %+v, want two objects", listOut)
	}
	for _, object := range listOut.Objects {
		if object.Hash != h1 && object.Hash != h2 {
			t.Fatalf("list reported %q, want %q or %q", object.Hash, h1, h2)
		}
	}

	// meta and stats read the packed store.
	out, code = run(t, mf, "meta", h1)
	if code != 0 || !strings.Contains(out, "size=3") {
		t.Fatalf("meta = (%q, %d), want size=3", out, code)
	}
	out, code = run(t, mf, "stats")
	if code != 0 || !strings.Contains(out, "2 objects") || !strings.Contains(out, "6 bytes") {
		t.Fatalf("stats = (%q, %d), want two objects of six bytes", out, code)
	}

	// verify works per object and as a whole-store sweep.
	out, code = run(t, mf, "verify", h1)
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("verify = (%q, %d), want ok", out, code)
	}
	out, code = run(t, mf, "verify", "--all")
	if code != 0 || !strings.Contains(out, "verified 2 objects, 0 corrupt") {
		t.Fatalf("verify --all = (%q, %d)", out, code)
	}

	// clean runs against the pack backend's own scratch state.
	out, code = run(t, mf, "clean")
	if code != 0 || !strings.Contains(out, "removed 0") {
		t.Fatalf("clean = (%q, %d)", out, code)
	}

	// gc reclaims the unreachable object through the portable sweep.
	out, code = run(t, mf, "gc", "--min-age", "0", h1)
	if code != 0 || !strings.Contains(out, "deleted 1 objects") {
		t.Fatalf("gc = (%q, %d), want one object deleted", out, code)
	}
	if _, code := run(t, mf, "get", h2); code != 1 {
		t.Fatalf("gc left the unreachable object behind: get exit %d, want 1", code)
	}
	if _, code := run(t, mf, "get", h1); code != 0 {
		t.Fatalf("gc deleted the reachable object: get exit %d", code)
	}
	out, code = run(t, mf, "stats")
	if code != 0 || !strings.Contains(out, "1 objects") {
		t.Fatalf("stats after gc = (%q, %d), want one object", out, code)
	}
}

// TestPackFSPruneDryRunAndDelete: prune reports a packed store's unreachable
// objects without deleting them by default, and deletes them with
// --dry-run=false.
func TestPackFSPruneDryRunAndDelete(t *testing.T) {
	mf := packFSMode(t)
	h1 := putFile(t, mf, "keep")
	h2 := putFile(t, mf, "drop")

	out, code := run(t, mf, "prune", "--min-age", "0", h1)
	if code != 0 || !strings.Contains(out, "would delete 1 objects") || !strings.Contains(out, strings.TrimPrefix(h2, "sha256:")) {
		t.Fatalf("prune dry run = (%q, %d), want %s reported", out, code, h2)
	}
	if _, code := run(t, mf, "get", h2); code != 0 {
		t.Fatalf("prune dry run deleted %s", h2)
	}

	out, code = run(t, mf, "prune", "--min-age", "0", "--dry-run=false", h1)
	if code != 0 || !strings.Contains(out, "deleted 1 objects") {
		t.Fatalf("prune = (%q, %d), want one object deleted", out, code)
	}
	if _, code := run(t, mf, "get", h2); code != 1 {
		t.Fatalf("prune left the unreachable object behind: get exit %d, want 1", code)
	}
}

// recordingBackend counts the Close calls the write path must make.
type recordingBackend struct {
	cas.Backend
	closes int
}

// Close records the call.
func (b *recordingBackend) Close() error {
	b.closes++
	return nil
}

// TestWritePathClosesStore: the path that writes closes the store it opened, so
// a backend holding a handle open for appends (packfs keeps its active pack
// file) is released before the command returns (cli.md §2).
func TestWritePathClosesStore(t *testing.T) {
	backend := &recordingBackend{Backend: memory.New()}
	st := &store.Store{Backend: backend, Kind: store.KindFS}
	spec, ok := command("put")
	if !ok {
		t.Fatal("put is not a registered command")
	}
	if err := runTargetOp(context.Background(), spec, st, []string{writeTemp(t, "closed on the write path")}); err != nil {
		t.Fatalf("put through the write path: %v", err)
	}
	if backend.closes != 1 {
		t.Fatalf("write path closed the store %d times, want exactly 1", backend.closes)
	}

	// An operation failure is still reported, and that store is still closed —
	// Close is idempotent, so each opened store gets its own backend.
	failing := &recordingBackend{Backend: memory.New()}
	failingStore := &store.Store{Backend: failing, Kind: store.KindFS}
	if err := runTargetOp(context.Background(), spec, failingStore, nil); err == nil {
		t.Fatal("put without an operand = nil, want a usage error")
	}
	if failing.closes != 1 {
		t.Fatalf("failed operation closed the store %d times, want exactly 1", failing.closes)
	}
}

// TestWebRefusesNonFilesystemBackend: the viewer needs the concrete filesystem
// backend, so a backend with no filesystem view is refused with an error naming
// the operation and the backend rather than reading a different directory than
// -store named (cli.md §2).
func TestWebRefusesNonFilesystemBackend(t *testing.T) {
	var logBuf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(previous)

	mf := modeFlags{store: t.TempDir(), backend: string(store.KindPackFS)}
	if code := runWeb(context.Background(), mf, []string{"-no-open"}); code != 1 {
		t.Fatalf("web -backend packfs exit = %d, want 1 (unsupported)", code)
	}
	for _, want := range []string{"web", string(store.KindPackFS)} {
		if !strings.Contains(logBuf.String(), want) {
			t.Fatalf("web -backend packfs logged %q, want it to name %q", logBuf.String(), want)
		}
	}

	// The subcommand's own -backend is honored the same way.
	logBuf.Reset()
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-backend", "packfs", "-no-open"}); code != 1 {
		t.Fatalf("web -backend packfs (command flag) exit = %d, want 1", code)
	}
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-backend", "sqlite", "-no-open"}); code != 2 {
		t.Fatalf("web -backend sqlite exit = %d, want 2 (usage)", code)
	}
}
