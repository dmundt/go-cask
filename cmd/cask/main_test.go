package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
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

// TestPutJSON covers the documented -json shape for put (cli §3).
func TestPutJSON(t *testing.T) {
	mf := localMF(t)
	out, code := run(t, mf, "put", "-json", writeTemp(t, "json put"))
	if code != 0 {
		t.Fatalf("put -json exit %d", code)
	}
	var got struct {
		Hash         string `json:"hash"`
		Deduplicated bool   `json:"deduplicated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("put -json output %q: %v", out, err)
	}
	if _, err := sha256.Parse(got.Hash); err != nil {
		t.Fatalf("put -json digest %q: %v", got.Hash, err)
	}
	if !strings.HasPrefix(got.Hash, "sha256:") {
		t.Fatalf("put -json digest %q must carry the printable %q prefix", got.Hash, "sha256:")
	}
	if got.Deduplicated {
		t.Fatal("first put must not be reported as deduplicated")
	}

	// A second put of the same bytes deduplicates and says so.
	out, code = run(t, mf, "put", "-json", writeTemp(t, "json put"))
	if code != 0 {
		t.Fatalf("second put -json exit %d", code)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("second put -json output %q: %v", out, err)
	}
	if !got.Deduplicated {
		t.Fatalf("second put not reported as deduplicated: %q", out)
	}
}

// TestListRejectsOutOfRangeFlags pins the CLI validation added for api-design
// §9 (reject rather than silently clamp).
func TestListRejectsOutOfRangeFlags(t *testing.T) {
	mf := localMF(t)
	for _, args := range [][]string{
		{"list", "-limit", "0"},
		{"list", "-limit", "1001"},
		{"list", "-offset", "-1"},
	} {
		if _, code := run(t, mf, args[0], args[1:]...); code != 2 {
			t.Errorf("%v: exit %d, want 2 (usage)", args, code)
		}
	}
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
	if _, err := sha256.Parse(h); err != nil {
		t.Fatalf("put printed invalid digest %q: %v", h, err)
	}

	// cat → stdout
	out, code = run(t, mf, "get", h)
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
	// list -json keeps the documented item fields: "hash" (the printable
	// sha256:hexdigest form), "algorithm" (the client's constant) and "size"
	// (cli §3). There is no -algo/--algo flag any more.
	out, code = run(t, mf, "list", "-json")
	if code != 0 {
		t.Fatalf("list -json exit %d", code)
	}
	var listOut struct {
		Total   int `json:"total"`
		Objects []struct {
			Hash      string `json:"hash"`
			Algorithm string `json:"algorithm"`
			Size      int64  `json:"size"`
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(out), &listOut); err != nil {
		t.Fatalf("list -json output %q: %v", out, err)
	}
	if listOut.Total != 1 || len(listOut.Objects) != 1 {
		t.Fatalf("list -json = %+v, want one object", listOut)
	}
	if it := listOut.Objects[0]; it.Hash != h || it.Algorithm != "sha256" || it.Size != 10 {
		t.Fatalf("list -json object = %+v, want hash %q algorithm sha256 size 10", it, h)
	}
	out, code = run(t, mf, "meta", h)
	if code != 0 || !strings.Contains(out, "size=10") {
		t.Fatalf("meta = (%q, %d), want size=10", out, code)
	}
	// meta -json prints the printable digest form and the constant algorithm.
	out, code = run(t, mf, "meta", "-json", h)
	if code != 0 {
		t.Fatalf("meta -json exit %d", code)
	}
	var metaOut map[string]any
	if err := json.Unmarshal([]byte(out), &metaOut); err != nil {
		t.Fatalf("meta -json output %q: %v", out, err)
	}
	if metaOut["hash"] != h || metaOut["algorithm"] != "sha256" || metaOut["size"] != float64(10) {
		t.Fatalf("meta -json = %v, want hash %q algorithm sha256 size 10", metaOut, h)
	}
	out, code = run(t, mf, "stats")
	if code != 0 || !strings.Contains(out, "1 objects") {
		t.Fatalf("stats = (%q, %d)", out, code)
	}
	// stats keeps the flat "N objects, M bytes" summary (no per-algorithm
	// counts: the core does not know the algorithm).
	if !strings.Contains(out, "10 bytes") {
		t.Fatalf("stats = %q, want the object/byte summary", out)
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

	// gc keeping h → h survives (gc is grace-gated; h is a root anyway).
	if _, code := run(t, mf, "gc", h); code != 0 {
		t.Fatalf("gc exit %d", code)
	}
	if _, code := run(t, mf, "get", h); code != 0 {
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

	// Forced sweep (--min-age 0): the unreferenced object h2 must go.
	if _, code := run(t, mf, "gc", "--min-age", "0", h1); code != 0 {
		t.Fatal("gc failed")
	}
	if _, code := run(t, mf, "get", h2); code == 0 {
		t.Fatal("unreferenced object survived gc")
	}
	if _, code := run(t, mf, "get", h1); code != 0 {
		t.Fatal("referenced object deleted")
	}
}

func TestExitCodes(t *testing.T) {
	mf := localMF(t)
	// No mode → usage (2).
	if _, code := run(t, modeFlags{}, "list"); code != 2 {
		t.Fatalf("no-mode exit = %d, want 2", code)
	}
	// Invalid digest → usage (2).
	if _, code := run(t, mf, "get", "not-a-digest"); code != 2 {
		t.Fatalf("bad-digest exit = %d, want 2", code)
	}
	// Missing object → runtime error (1).
	missing, err := sha256.Parse("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, mf, "get", sha256.Format(missing)); code != 1 {
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

func TestClean(t *testing.T) {
	mf := localMF(t)
	out, code := run(t, mf, "clean")
	if code != 0 || !strings.Contains(out, "removed 0") {
		t.Fatalf("clean = (%q, %d)", out, code)
	}
}

func TestPruneRequiresRoot(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "prune"); code != 2 {
		t.Fatalf("prune without root: exit %d, want 2 (usage)", code)
	}
}

func TestPruneForcedDeletesUnreferenced(t *testing.T) {
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

	// Forced prune (--min-age 0, --dry-run=false): unreferenced h2 must go.
	if _, code := run(t, mf, "prune", "--min-age", "0", "--dry-run=false", h1); code != 0 {
		t.Fatal("prune failed")
	}
	if _, code := run(t, mf, "get", h2); code == 0 {
		t.Fatal("unreferenced object survived prune")
	}
	if _, code := run(t, mf, "get", h1); code != 0 {
		t.Fatal("referenced object deleted")
	}
}
