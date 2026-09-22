package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
	"github.com/dmundt/go-cask/internal/index"
)

func TestViewerHasher(t *testing.T) {
	for _, name := range []string{sha256.Name, sha512.Name, sha512256.Name} {
		hasher, err := viewerHasher(name)
		if err != nil || hasher == nil {
			t.Fatalf("viewerHasher(%q) = (%T, %v), want hasher", name, hasher, err)
		}
	}
	if _, err := viewerHasher("unknown"); err == nil {
		t.Fatal("viewerHasher(unknown) accepted unsupported algorithm")
	}
}

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

func TestSeedPreview(t *testing.T) {
	mf := localMF(t)
	out, code := run(t, mf, "seed-preview", "-count", "6")
	if code != 0 || out != "preview objects: added 6, deduplicated 0\n" {
		t.Fatalf("first seed-preview = (%q, %d)", out, code)
	}
	raw, err := fs.New(mf.store)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := raw.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 6 {
		t.Fatalf("seeded objects = %d, want 6", len(digests))
	}
	references, err := previewReferences(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if references == nil {
		t.Fatal("preview references = nil")
	}
	objects := make([]cas.Digest, 0, 6)
	for ordinal := range 6 {
		object := previewObjectFor(ordinal, objects)
		objects = append(objects, object.digest)
	}
	if got := references.Outbound(objects[3]); len(got) != 3 || !got[0].Equal(objects[2]) || !got[1].Equal(objects[1]) || !got[2].Equal(objects[0]) {
		t.Fatalf("preview outbound = %v, want [%s %s %s]", got, objects[2], objects[1], objects[0])
	}
	for _, test := range []struct {
		digest cas.Digest
		want   int
	}{
		{objects[0], 3},
		{objects[4], 1},
		{objects[5], 0},
	} {
		if got := len(references.Inbound(test.digest)); got != test.want {
			t.Fatalf("preview inbound %s = %d, want %d", test.digest, got, test.want)
		}
	}
	for ordinal, want := range map[int]bool{
		4: false,
		5: true,
		6: true,
		7: true,
	} {
		if got := previewDetachedOrdinal(ordinal); got != want {
			t.Fatalf("previewDetachedOrdinal(%d) = %v, want %v", ordinal, got, want)
		}
	}
	for _, test := range []struct {
		digest cas.Digest
		want   bool
	}{
		{objects[0], true},
		{objects[3], true},
		{objects[4], false},
		{objects[5], false},
	} {
		if got := references.IsReachable(test.digest); got != test.want {
			t.Fatalf("preview reachability %s = %t, want %t", test.digest, got, test.want)
		}
	}
	for _, digest := range digests {
		rc, err := raw.Get(context.Background(), digest)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if typ := index.EnvelopeType(data); typ == "" {
			t.Fatalf("seeded object %s lacks a valid envelope type", digest)
		}
	}

	// Ordinal 1 is seeded with tampered bytes and sits inside a reachable
	// block, so the browser gets corrupt objects that are not orphaned.
	if !previewCorruptOrdinal(1) || previewCorruptOrdinal(0) {
		t.Fatalf("unexpected corrupt ordinal selection")
	}
	if !references.IsReachable(objects[1]) {
		t.Fatalf("corrupt preview object %s must stay reachable", objects[1])
	}
	if err := raw.Verify(context.Background(), objects[1], sha256.New()); err == nil {
		t.Fatalf("corrupt preview object %s must fail verification", objects[1])
	}
	if err := raw.Verify(context.Background(), objects[0], sha256.New()); err != nil {
		t.Fatalf("intact preview object %s must verify: %v", objects[0], err)
	}

	out, code = run(t, mf, "seed-preview", "-count", "6")
	if code != 0 || out != "preview objects: added 0, deduplicated 6\n" {
		t.Fatalf("second seed-preview = (%q, %d)", out, code)
	}
	for _, args := range [][]string{{"-count", "0"}, {"-count", "10001"}, {"unexpected"}} {
		if _, code := run(t, mf, "seed-preview", args...); code != 2 {
			t.Fatalf("seed-preview %v exit = %d, want 2", args, code)
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

func TestVerifyAll(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "verified")); code != 0 {
		t.Fatal("put failed")
	}
	out, code := run(t, mf, "verify", "--all")
	if code != 0 || !strings.Contains(out, "verified 1 objects, 0 corrupt") {
		t.Fatalf("verify --all = (%q, %d)", out, code)
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

func TestParseGlobalAndMaintenanceOps(t *testing.T) {
	t.Run("parseGlobal", func(t *testing.T) {
		cases := []struct {
			name    string
			args    []string
			wantMF  modeFlags
			wantCmd string
			wantErr bool
		}{
			{name: "command no store", args: []string{"put", "file.bin"}, wantMF: modeFlags{}, wantCmd: "put"},
			{name: "store option", args: []string{"-store", "/tmp/repo", "list"}, wantMF: modeFlags{store: "/tmp/repo"}, wantCmd: "list"},
			{name: "missing store arg", args: []string{"-store"}, wantErr: true},
			{name: "no command", args: nil, wantErr: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotMF, gotCmd, _, err := parseGlobal(tc.args)
				if tc.wantErr {
					if err == nil {
						t.Fatal("parseGlobal() error = nil, want error")
					}
					return
				}
				if err != nil {
					t.Fatalf("parseGlobal() unexpected error: %v", err)
				}
				if gotMF != tc.wantMF || gotCmd != tc.wantCmd {
					t.Fatalf("parseGlobal(%v) = (%+v, %q), want (%+v, %q)", tc.args, gotMF, gotCmd, tc.wantMF, tc.wantCmd)
				}
			})
		}
	})

	t.Run("maintenanceOp", func(t *testing.T) {
		for _, tc := range []struct {
			cmd  string
			want bool
		}{
			{"gc", true},
			{"prune", true},
			{"clean", true},
			{"put", false},
			{"list", false},
		} {
			if got := maintenanceOp(tc.cmd); got != tc.want {
				t.Fatalf("maintenanceOp(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		}
	})
}

func TestLocalPutDedupAndDigestParsing(t *testing.T) {
	mf := localMF(t)
	t.Run("localPut dedups", func(t *testing.T) {
		raw, err := fs.New(mf.store)
		if err != nil {
			t.Fatal(err)
		}
		content := strings.NewReader("same bytes")
		h1, dup1, err := localPut(context.Background(), raw, content)
		if err != nil || dup1 {
			t.Fatalf("first localPut = (%v, %v, %v), want (hash, false, nil)", h1, dup1, err)
		}
		h2, dup2, err := localPut(context.Background(), raw, strings.NewReader("same bytes"))
		if err != nil || !dup2 || h1.String() != h2.String() {
			t.Fatalf("second localPut = (%v, %v, %v), want equal digest and dedup true", h2, dup2, err)
		}
	})

	t.Run("parseDigests rejects bad input", func(t *testing.T) {
		if _, err := parseDigests([]string{"bad-digest"}); err == nil {
			t.Fatal("parseDigests accepted invalid digest")
		}
	})
}

func TestVersionAndWebHelpers(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		out := captureStdout(t, func() { runVersion() })
		if !strings.Contains(out, "cask") || !strings.Contains(out, "go ") {
			t.Fatalf("version output = %q, want cask + go line", out)
		}
	})

	t.Run("bind and token helpers", func(t *testing.T) {
		for _, tc := range []struct {
			addr string
			want bool
		}{
			{"127.0.0.1:8080", true},
			{"[::1]:8080", true},
			{"localhost:8080", true},
			{"0.0.0.0:8080", false},
			{"192.168.1.10:8080", false},
			{"example.test:8080", false},
			{"no-port", false},
		} {
			if got := isLoopbackBind(tc.addr); got != tc.want {
				t.Fatalf("isLoopbackBind(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		}
		if tok := randomToken(); len(tok) == 0 || strings.Count(tok, "-") != 2 {
			t.Fatalf("randomToken() = %q, want 3 groups separated by dashes", tok)
		}
	})

	t.Run("browser command per platform", func(t *testing.T) {
		for _, tc := range []struct {
			goos string
			cmd  string
			args []string
		}{
			{"windows", "cmd", []string{"/c", "start", "http://x"}},
			{"darwin", "open", []string{"http://x"}},
			{"linux", "xdg-open", []string{"http://x"}},
		} {
			cmd, args := browserCommand(tc.goos, "http://x")
			if cmd != tc.cmd || !slices.Equal(args, tc.args) {
				t.Fatalf("browserCommand(%q) = (%q, %v), want (%q, %v)", tc.goos, cmd, args, tc.cmd, tc.args)
			}
		}
	})

	t.Run("runWeb", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-time.After(150 * time.Millisecond)
			cancel()
		}()
		if code := runWeb(ctx, modeFlags{store: t.TempDir()}, []string{"-bind", "127.0.0.1:0", "-no-open", "-allow-insecure-bind"}); code != 0 {
			t.Fatalf("runWeb exit = %d, want 0", code)
		}
	})
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPruneRequiresRoot(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "prune"); code != 2 {
		t.Fatalf("prune without root: exit %d, want 2 (usage)", code)
	}
}

func TestMaintenanceRejectsNegativeMinAge(t *testing.T) {
	mf := localMF(t)
	for _, command := range []string{"gc", "prune", "clean"} {
		args := []string{"--min-age", "-1s"}
		if command != "clean" {
			args = append(args, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
		}
		if _, code := run(t, mf, command, args...); code != 2 {
			t.Errorf("%s negative min-age: exit %d, want 2", command, code)
		}
	}
}

func TestVerifyRejectsExtraArguments(t *testing.T) {
	mf := localMF(t)
	for _, args := range [][]string{
		{"--all", "extra"},
		{"sha256:0000000000000000000000000000000000000000000000000000000000000000", "extra"},
	} {
		if _, code := run(t, mf, "verify", args...); code != 2 {
			t.Errorf("verify %v: exit %d, want 2", args, code)
		}
	}
}

func TestWebRejectsInvalidFlags(t *testing.T) {
	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{"-unknown"}); code != 2 {
		t.Fatalf("runWeb invalid flag exit = %d, want 2", code)
	}
}

func TestWebReportsListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	if code := runWeb(context.Background(), modeFlags{store: t.TempDir()}, []string{
		"-bind", listener.Addr().String(),
		"-no-open",
	}); code != 1 {
		t.Fatalf("runWeb occupied bind exit = %d, want 1", code)
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
