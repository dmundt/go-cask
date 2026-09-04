package cas

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustFS(t *testing.T, opts ...FSOption) *FSRawStore {
	s, err := NewFSRawStore(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// --- Fan-out layouts (cas-core §4.4; testing-strategy §1 layout laws) ---

func TestFanOutLayouts(t *testing.T) {
	digest := strings.Repeat("a1", 32) // 64 hex chars
	h, _ := ParseHash("sha256:" + digest)
	cases := []struct {
		name     string
		opts     []FSOption
		wantPath string
	}{
		{"flat", []FSOption{WithFanOut(0), WithFanLevels(0)}, filepath.Join("sha256", digest)},
		{"gitlike-default", nil, filepath.Join("sha256", "a1", digest)},
		{"deep-2-2", []FSOption{WithFanOut(2), WithFanLevels(2)}, filepath.Join("sha256", "a1", "a1", digest)},
		{"wide-4-1", []FSOption{WithFanOut(4), WithFanLevels(1)}, filepath.Join("sha256", "a1a1", digest)},
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		got := s.hashPath(h)
		want := filepath.Join(s.base, tc.wantPath)
		if got != want {
			t.Errorf("%s: hashPath = %q, want %q", tc.name, got, want)
		}
	}
}

func TestFanOutBounds(t *testing.T) {
	for _, tc := range []struct {
		opts []FSOption
		ok   bool
	}{
		{[]FSOption{WithFanOut(0), WithFanLevels(0)}, true},
		{[]FSOption{WithFanOut(33), WithFanLevels(2)}, false}, // 66 > 64
		{[]FSOption{WithFanOut(64), WithFanLevels(1)}, true},
		{[]FSOption{WithFanOut(-1)}, false},
		{[]FSOption{WithFanLevels(-1)}, false},
	} {
		_, err := NewFSRawStore(t.TempDir(), tc.opts...)
		if tc.ok && err != nil {
			t.Errorf("opts %v: unexpected error %v", tc.opts, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("opts %v: expected rejection", tc.opts)
		}
	}
}

// CAS law: layout equivalence — same content addressable under every layout.
func TestLayoutEquivalence(t *testing.T) {
	ctx := context.Background()
	content := []byte("the same bytes")
	h, _ := hashData("sha256", content)
	layouts := [][]FSOption{
		nil,
		{WithFanOut(0), WithFanLevels(0)},
		{WithFanOut(2), WithFanLevels(2)},
		{WithFanOut(4), WithFanLevels(1)},
	}
	for _, opts := range layouts {
		s := mustFS(t, opts...)
		if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
			t.Fatalf("opts %v: Put: %v", opts, err)
		}
		rc, err := s.Get(ctx, h)
		if err != nil {
			t.Fatalf("opts %v: Get: %v", opts, err)
		}
		got, err := readAllAndClose(rc)
		if err != nil || string(got) != string(content) {
			t.Fatalf("opts %v: read = %q, %v", opts, got, err)
		}
	}
}

// CAS law: path round-trip — pathToHash(hashPath(h)) == h (every layout).
func TestPathRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, algo := range []string{"sha256", "sha1"} {
		h, _ := hashData(algo, []byte("path round trip"))
		for _, opts := range [][]FSOption{nil, {WithFanOut(0), WithFanLevels(0)}, {WithFanOut(2), WithFanLevels(2)}, {WithFanOut(4), WithFanLevels(1)}} {
			s := mustFS(t, opts...)
			if err := s.Put(ctx, h, strings.NewReader("path round trip")); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(s.base, s.hashPath(h))
			if err != nil {
				t.Fatal(err)
			}
			back, err := pathToHash(rel)
			if err != nil {
				t.Fatalf("pathToHash(%q): %v", rel, err)
			}
			if !back.Equal(h) {
				t.Fatalf("pathToHash(hashPath(h)) != h: %s vs %s", back, h)
			}
		}
	}
}

// --- Atomic writes & .tmp handling ---

func TestTmpFilesIgnored(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("data"))
	if err := s.Put(ctx, h, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed write: a leftover .tmp next to the object.
	tmp := s.hashPath(h) + ".tmp"
	if err := os.WriteFile(tmp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Equal(h) {
		t.Fatalf("List with leftover tmp = %v", list)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 1 {
		t.Fatalf("Stats with leftover tmp = %+v", st)
	}
}

// --- Verify (integrity) ---

func TestVerify(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	content := []byte("verify me please")
	h, _ := hashData("sha256", content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err != nil {
		t.Fatalf("Verify on intact object: %v", err)
	}
	// Single flipped byte at first, middle, last position → ErrHashMismatch.
	path := s.hashPath(h)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range []int{0, len(data) / 2, len(data) - 1} {
		corrupt := make([]byte, len(data))
		copy(corrupt, data)
		corrupt[pos] ^= 0xff
		if err := os.WriteFile(path, corrupt, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(ctx, h); !errors.Is(err, ErrHashMismatch) {
			t.Errorf("Verify after flip at %d: err = %v, want ErrHashMismatch", pos, err)
		}
	}
	// Missing object → ErrNotFound.
	missing, _ := hashData("sha256", []byte("missing"))
	if err := s.Verify(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Verify(missing) = %v, want ErrNotFound", err)
	}
	// Restore: Verify passes again.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err != nil {
		t.Fatalf("Verify after restore: %v", err)
	}
}

// Verify must also work for algorithms registered as one-shot HashFunc only
// (the buffered fallback path).
func TestVerifyCustomAlgorithmFallback(t *testing.T) {
	RegisterHash("verify-test", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		return hash{algo: "verify-test", bytes: sum[:]}
	})
	ctx := context.Background()
	s := mustFS(t)
	h, err := hashData("verify-test", []byte("custom algo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, h, strings.NewReader("custom algo")); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err != nil {
		t.Fatalf("Verify(custom algo): %v", err)
	}
}

// --- GC (mark-and-sweep) & Prune (age-based) ---

func TestGC(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	keep, _ := hashData("sha256", []byte("keep"))
	drop, _ := hashData("sha256", []byte("drop"))
	if err := s.Put(ctx, keep, strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, drop, strings.NewReader("drop")); err != nil {
		t.Fatal(err)
	}
	// Empty reachable set → deletes everything.
	if err := s.GC(ctx, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, keep); ok {
		t.Fatal("GC with empty roots must delete everything")
	}
	// All-reachable → deletes nothing.
	if err := s.Put(ctx, keep, strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, drop, strings.NewReader("drop")); err != nil {
		t.Fatal(err)
	}
	if err := s.GC(ctx, map[string]bool{keep.String(): true, drop.String(): true}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Stats(ctx); n.ObjectCount != 2 {
		t.Fatalf("GC all-reachable deleted objects: %+v", n)
	}
	// Partial: keep only one.
	if err := s.GC(ctx, map[string]bool{keep.String(): true}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, keep); !ok {
		t.Fatal("reachable object deleted")
	}
	if ok, _ := s.Exists(ctx, drop); ok {
		t.Fatal("unreachable object survived GC")
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	root, _ := hashData("sha256", []byte("root"))
	young, _ := hashData("sha256", []byte("young garbage"))
	old, _ := hashData("sha256", []byte("old garbage"))
	for _, h := range []Hash{root, young, old} {
		content := []byte(h.String()) // content irrelevant; hash is the key
		if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
			t.Fatal(err)
		}
	}
	// Age the "old" object by backdating its mtime.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(s.hashPath(old), past, past); err != nil {
		t.Fatal(err)
	}

	// dry-run: returns the would-be-deleted set, deletes nothing.
	doomed, err := s.Prune(ctx, []Hash{root}, time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 1 || !doomed[0].Equal(old) {
		t.Fatalf("dry-run doomed = %v, want [%s]", doomed, old)
	}
	if ok, _ := s.Exists(ctx, old); !ok {
		t.Fatal("dry-run must not delete")
	}

	// Real run: only the unreachable object older than minAge is deleted.
	doomed, err = s.Prune(ctx, []Hash{root}, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 1 {
		t.Fatalf("prune doomed = %v", doomed)
	}
	if ok, _ := s.Exists(ctx, root); !ok {
		t.Fatal("root deleted by prune")
	}
	if ok, _ := s.Exists(ctx, young); !ok {
		t.Fatal("young unreachable object must be kept (grace period)")
	}
	if ok, _ := s.Exists(ctx, old); ok {
		t.Fatal("old unreachable object survived prune")
	}
}

// --- Stats ---

func TestStats(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	h1, _ := hashData("sha256", []byte("aaaa"))
	h2, _ := hashData("sha256", []byte("bbbb"))
	h3, _ := hashData("sha1", []byte("cccc"))
	for _, h := range []Hash{h1, h2, h3} {
		if err := s.Put(ctx, h, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 3 {
		t.Fatalf("ObjectCount = %d", st.ObjectCount)
	}
	if st.AlgorithmCounts["sha256"] != 2 || st.AlgorithmCounts["sha1"] != 1 {
		t.Fatalf("AlgorithmCounts = %v", st.AlgorithmCounts)
	}
	if st.TotalSize <= 0 {
		t.Fatalf("TotalSize = %d", st.TotalSize)
	}
	if !strings.Contains(st.String(), "3 objects") {
		t.Fatalf("String() = %q", st.String())
	}
}

// --- Concurrency (performance §2; testing-strategy §4.4) ---

func TestConcurrentSameHashPut(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("same"))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Put(ctx, h, strings.NewReader("same")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rc, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := readAllAndClose(rc)
	if string(got) != "same" {
		t.Fatalf("content after concurrent puts = %q", got)
	}
}

// TestUniqueTempAcrossInstances simulates two OS processes writing the SAME
// hash concurrently: two FSRawStore instances over one directory each have
// their own mutex (the in-process lock does not coordinate them), so safety
// relies on the unique per-writer temp names (cas-core §4.4). The object
// must never be corrupted and no `*.tmp` may survive. Transient Put errors
// are tolerated: on Windows a concurrent rename-over-existing can return
// Access denied (no atomic last-wins across processes), so a racing Put may
// fail — but it must clean up its temp and never corrupt the object.
func TestUniqueTempAcrossInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s1, err := NewFSRawStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewFSRawStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := hashData("sha256", []byte("cross-process"))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			_ = s1.Put(ctx, h, strings.NewReader("cross-process")) // errors tolerated mid-race
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			_ = s2.Put(ctx, h, strings.NewReader("cross-process")) // errors tolerated mid-race
		}
	}()
	wg.Wait()

	rc, err := s1.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := readAllAndClose(rc)
	if string(got) != "cross-process" {
		t.Fatalf("content after cross-instance puts = %q (corrupted)", got)
	}
	if leftovers := tmpFilesIn(s1, h); len(leftovers) != 0 {
		t.Fatalf("temp files left after concurrent puts: %v", leftovers)
	}
}

func TestConcurrentGetDuringDelete(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("race"))
	if err := s.Put(ctx, h, strings.NewReader("race")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := s.Get(ctx, h)
			if err != nil {
				return // ErrNotFound is acceptable mid-race
			}
			rc.Close()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s.Delete(ctx, h)
			s.Put(ctx, h, strings.NewReader("race"))
		}
	}()
	wg.Wait()
}

func TestParallelListStatsDuringWrites(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, _ := hashData("sha256", []byte(fmt.Sprintf("obj-%d", i)))
			for j := 0; j < 25; j++ {
				s.Put(ctx, h, strings.NewReader(fmt.Sprintf("obj-%d", i)))
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				s.List(ctx, "")
				s.Stats(ctx)
			}
		}()
	}
	wg.Wait()
}

// --- Fuzz targets (testing-strategy §4.3; run by CI) ---

// FuzzPathRoundTrip: arbitrary digest + layout → hashPath → pathToHash
// round-trips.
func FuzzPathRoundTrip(f *testing.F) {
	f.Add("sha256", []byte{1, 2, 3, 4}, uint8(2), uint8(1))
	f.Add("sha1", []byte{9}, uint8(0), uint8(0))
	f.Fuzz(func(t *testing.T, algo string, digest []byte, fanOut, fanLevels uint8) {
		if len(digest) == 0 {
			t.Skip()
		}
		hexDigest := fmt.Sprintf("%x", digest)
		h, err := ParseHash(algo + ":" + hexDigest)
		if err != nil {
			t.Skip() // unknown algo / malformed
		}
		if int(fanOut)*int(fanLevels) > MaxFanDepth {
			t.Skip() // layout would be rejected by the constructor
		}
		s, err := NewFSRawStore(t.TempDir(), WithFanOut(int(fanOut)), WithFanLevels(int(fanLevels)))
		if err != nil {
			t.Skip()
		}
		rel, err := filepath.Rel(s.base, s.hashPath(h))
		if err != nil {
			t.Fatal(err)
		}
		back, err := pathToHash(rel)
		if err != nil {
			t.Fatalf("pathToHash(%q): %v", rel, err)
		}
		if !back.Equal(h) {
			t.Fatalf("round-trip mismatch: %s vs %s", back, h)
		}
	})
}

// FuzzVerify: corrupted bytes must fail Verify with ErrHashMismatch.
func FuzzVerify(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		s := mustFS(t)
		h, err := hashData("sha256", data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(context.Background(), h, strings.NewReader(string(data))); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(context.Background(), h); err != nil {
			t.Fatalf("Verify on intact data: %v", err)
		}
		path := s.hashPath(h)
		stored, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) == 0 {
			return
		}
		corrupt := make([]byte, len(stored))
		copy(corrupt, stored)
		corrupt[0] ^= 0xff
		if err := os.WriteFile(path, corrupt, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(context.Background(), h); !errors.Is(err, ErrHashMismatch) {
			t.Fatalf("corrupted object: err = %v, want ErrHashMismatch", err)
		}
	})
}

// ExampleNewFSRawStore demonstrates the default Git-like fan-out store.
func ExampleNewFSRawStore() {
	s, err := NewFSRawStore("./objects")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll("./objects")
	_ = s
}

// TestSizeAndClean exercises FSRawStore.Size and the *.tmp sweep.
func TestSizeAndClean(t *testing.T) {
	ctx := context.Background()
	s, err := NewFSRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashData("sha256", []byte("size me"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, h, strings.NewReader("size me")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Size(ctx, h)
	if err != nil || got != 7 {
		t.Fatalf("Size = %d, %v; want 7", got, err)
	}
	missing, _ := hashData("sha256", []byte("missing"))
	if _, err := s.Size(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Size(missing) err = %v, want ErrNotFound", err)
	}

	// Two leftover *.tmp files: one old, one fresh. Both live in the
	// fan-out dir of an existing object (where a crash mid-write leaves
	// them), so store the second object first.
	other, _ := hashData("sha256", []byte("other"))
	if err := s.Put(ctx, other, strings.NewReader("other")); err != nil {
		t.Fatal(err)
	}
	oldTmp := s.hashPath(h) + ".tmp"
	freshTmp := s.hashPath(other) + ".tmp"
	if err := os.WriteFile(oldTmp, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldTmp, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshTmp, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sweep older than 1h: only the old file goes.
	n, err := s.Clean(ctx, time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("Clean(1h) = %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(oldTmp); !os.IsNotExist(err) {
		t.Fatal("old tmp still present")
	}
	if _, err := os.Stat(freshTmp); err != nil {
		t.Fatal("fresh tmp wrongly removed")
	}

	// olderThan <= 0 removes everything.
	n, err = s.Clean(ctx, 0)
	if err != nil || n != 1 {
		t.Fatalf("Clean(0) = %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(freshTmp); !os.IsNotExist(err) {
		t.Fatal("fresh tmp still present after Clean(0)")
	}
}

// TestWithDirSyncPut verifies the optional parent-directory fsync is a no-op
// on platforms that cannot sync directories, and otherwise succeeds.
func TestWithDirSyncPut(t *testing.T) {
	s, err := NewFSRawStore(t.TempDir(), WithDirSync())
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashData("sha256", []byte("durable"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, h, strings.NewReader("durable")); err != nil {
		t.Fatalf("Put with WithDirSync: %v", err)
	}
	got, err := s.Size(ctx, h)
	if err != nil || got != 7 {
		t.Fatalf("Size = %d, %v; want 7", got, err)
	}
}
