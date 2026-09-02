package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// run executes a cask operation in-process, returning its stdout and exit
// code (0/1/2 per cli §3).
func run(t *testing.T, mf modeFlags, cmd string, args ...string) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := runOp(context.Background(), mf, cmd, args)
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}

func localMF(t *testing.T) modeFlags {
	return modeFlags{store: t.TempDir()}
}

func TestLocalRoundTrip(t *testing.T) {
	mf := localMF(t)
	f := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(f, []byte("hello cask"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, mf, "put", f)
	if code != 0 {
		t.Fatalf("put exit %d", code)
	}
	h := strings.TrimSpace(out)
	if _, err := cas.ParseHash(h); err != nil {
		t.Fatalf("put printed invalid hash %q: %v", h, err)
	}

	// cat → stdout
	out, code = run(t, mf, "cat", h)
	if code != 0 || out != "hello cask" {
		t.Fatalf("cat = (%q, %d), want hello cask", out, code)
	}

	// get → file
	dest := filepath.Join(t.TempDir(), "out.bin")
	if _, code := run(t, mf, "get", h, "-o", dest); code != 0 {
		t.Fatalf("get -o exit %d", code)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "hello cask" {
		t.Fatalf("get -o = %q, %v", got, err)
	}

	// list + meta + stats
	out, code = run(t, mf, "list")
	if code != 0 || !strings.Contains(out, h) {
		t.Fatalf("list = (%q, %d)", out, code)
	}
	out, code = run(t, mf, "meta", h)
	if code != 0 || !strings.Contains(out, "size=10") {
		t.Fatalf("meta = (%q, %d), want size=10", out, code)
	}
	out, code = run(t, mf, "stats")
	if code != 0 || !strings.Contains(out, "1 objects") {
		t.Fatalf("stats = (%q, %d)", out, code)
	}

	// verify ok
	if _, code := run(t, mf, "verify", h); code != 0 {
		t.Fatalf("verify exit %d", code)
	}

	// prune dry-run default
	out, code = run(t, mf, "prune", "--min-age", "1ns", h)
	if code != 0 || !strings.Contains(out, "dry-run") {
		t.Fatalf("prune = (%q, %d), want dry-run", out, code)
	}

	// gc keeping h → h survives
	if _, code := run(t, mf, "gc", h); code != 0 {
		t.Fatalf("gc exit %d", code)
	}
	if _, code := run(t, mf, "cat", h); code != 0 {
		t.Fatalf("cat after gc exit %d", code)
	}
}

func TestLocalGcDeletesUnreferenced(t *testing.T) {
	mf := localMF(t)
	h1, code := run(t, mf, "put", writeTemp(t, "one"))
	if code != 0 {
		t.Fatal("put one failed")
	}
	h2, code := run(t, mf, "put", writeTemp(t, "two"))
	if code != 0 {
		t.Fatal("put two failed")
	}
	h1, h2 = strings.TrimSpace(h1), strings.TrimSpace(h2)

	if _, code := run(t, mf, "gc", h1); code != 0 {
		t.Fatal("gc failed")
	}
	if _, code := run(t, mf, "cat", h2); code == 0 {
		t.Fatal("unreferenced object survived gc")
	}
	if _, code := run(t, mf, "cat", h1); code != 0 {
		t.Fatal("referenced object deleted")
	}
}

func TestExitCodes(t *testing.T) {
	mf := localMF(t)
	// No mode → usage (2).
	if _, code := run(t, modeFlags{}, "list"); code != 2 {
		t.Fatalf("no-mode exit = %d, want 2", code)
	}
	// Invalid hash → usage (2).
	if _, code := run(t, mf, "cat", "not-a-hash"); code != 2 {
		t.Fatalf("bad-hash exit = %d, want 2", code)
	}
	// Missing object → runtime error (1).
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, code := run(t, mf, "cat", missing.String()); code != 1 {
		t.Fatalf("missing-object exit = %d, want 1", code)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}
