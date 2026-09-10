package memory

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

func TestMemoryBackend(t *testing.T) {
	ctx := context.Background()
	b := New()
	h := cas.HashBytes([]byte("hello"))
	if err := b.Put(ctx, h, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	rc, err := b.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllAndClose(rc)
	if err != nil || string(got) != "hello" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	ok, err := b.Exists(ctx, h)
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if err := b.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, h); err == nil {
		t.Fatal("Get after Delete must error")
	}
}

func TestMemoryBackendMissing(t *testing.T) {
	ctx := context.Background()
	b := New()
	h := cas.HashBytes([]byte("missing"))
	_, err := b.Get(ctx, h)
	if err == nil {
		t.Fatal("Get(missing) must error")
	}
	if err := b.Delete(ctx, h); err != nil {
		t.Fatal("Delete(missing) must be no-op")
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestMemoryBackendSuite covers the in-memory backend contract directly:
// round-trip, idempotence, filtering, error paths, and canceled contexts.
func TestMemoryBackendSuite(t *testing.T) {
	m := New()
	ctx := context.Background()
	h1 := cas.HashBytes([]byte("alpha"))
	h2 := cas.HashBytes([]byte("beta"))

	if err := m.Put(ctx, h1, strings.NewReader("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(ctx, h1, strings.NewReader("alpha")); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := m.Put(ctx, h2, strings.NewReader("beta")); err != nil {
		t.Fatal(err)
	}
	rc, err := m.Get(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	data, err := readAllAndClose(rc)
	if err != nil || string(data) != "alpha" {
		t.Fatalf("Get = %q, %v", data, err)
	}
	missing := cas.HashBytes([]byte("missing"))
	if _, err := m.Get(ctx, missing); err == nil {
		t.Fatal("Get(missing) must error")
	}
	if ok, _ := m.Exists(ctx, missing); ok {
		t.Fatal("Exists(missing) = true")
	}
	if err := m.Delete(ctx, missing); err != nil { // missing is a no-op
		t.Fatal(err)
	}
	if err := m.Delete(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.Exists(ctx, h2); ok {
		t.Fatal("deleted object still present")
	}
	all, err := m.List(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
	if all[0].Algorithm() != "sha256" {
		t.Fatalf("List[0] algorithm = %q", all[0].Algorithm())
	}
	if got, _ := m.List(ctx, "sha1"); len(got) != 0 {
		t.Fatalf("List(sha1) = %v, want empty", got)
	}

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { return m.Put(cctx, h1, strings.NewReader("x")) }},
		{"Get", func() error { _, err := m.Get(cctx, h1); return err }},
		{"Exists", func() error { _, err := m.Exists(cctx, h1); return err }},
		{"Delete", func() error { return m.Delete(cctx, h1) }},
		{"List", func() error { _, err := m.List(cctx, ""); return err }},
	} {
		t.Run("canceled/"+tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatalf("err = nil, want context.Canceled")
			}
		})
	}

	// Put with a failing reader leaves no entry behind.
	if err := m.Put(context.Background(), h1, errReader{err: io.ErrUnexpectedEOF}); err == nil {
		t.Fatal("Put with failing reader must error")
	}
	if ok, _ := m.Exists(ctx, h1); !ok {
		t.Fatal("previous entry must survive the failed Put")
	}
}

func TestMemoryBackendWithMaxSize(t *testing.T) {
	ctx := context.Background()
	b := New(WithMaxSize(5))             // cap of 5 bytes
	h1 := cas.HashBytes([]byte("hello")) // 5 bytes fits
	if err := b.Put(ctx, h1, strings.NewReader("hello")); err != nil {
		t.Fatalf("Put within cap = %v", err)
	}
	// A second distinct object pushes over the cap -> rejected.
	h2 := cas.HashBytes([]byte("world")) // another 5 bytes
	if err := b.Put(ctx, h2, strings.NewReader("world")); err == nil {
		t.Fatal("Put over cap must error")
	}
	// Re-Put of the same hash is idempotent and fits.
	if err := b.Put(ctx, h1, strings.NewReader("hello")); err != nil {
		t.Fatalf("re-Put within cap = %v", err)
	}
	// Delete frees space, so a subsequent Put fits again.
	if err := b.Delete(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, h2, strings.NewReader("world")); err != nil {
		t.Fatalf("Put after delete freeing space = %v", err)
	}
}

// countingReader yields n bytes and records how many were actually served.
type countingReader struct {
	n    int
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= c.n {
		return 0, io.EOF
	}
	chunk := min(len(p), c.n-c.read)
	for i := 0; i < chunk; i++ {
		p[i] = 'x'
	}
	c.read += chunk
	return chunk, nil
}

// TestMemoryBackendMaxSizeBoundsBuffering pins the documented "checked before
// allocation" behavior: an oversized Put must be rejected without buffering
// the whole object first.
func TestMemoryBackendMaxSizeBoundsBuffering(t *testing.T) {
	ctx := context.Background()
	const capBytes = 16
	b := New(WithMaxSize(capBytes))
	h := cas.HashBytes([]byte("whatever"))
	src := &countingReader{n: 1 << 20}
	if err := b.Put(ctx, h, src); err == nil {
		t.Fatal("Put over cap must error")
	}
	if src.read > capBytes+1 {
		t.Fatalf("Put buffered %d bytes for a %d-byte cap; want at most %d", src.read, capBytes, capBytes+1)
	}
	if st, err := b.Stats(ctx); err != nil || st.ObjectCount != 0 {
		t.Fatalf("rejected Put stored objects: %+v, %v", st, err)
	}
}

// TestMemoryBackendStats verifies Stats recomputes per-algorithm counts, the
// total stored byte size, and the object count from the live map.
func TestMemoryBackendStats(t *testing.T) {
	ctx := context.Background()
	b := New()
	// Three objects of different sizes under the core's one algorithm.
	h1 := cas.HashBytes([]byte("alpha"))
	h2 := cas.HashBytes([]byte("a-longer-beta-payload"))
	h3 := cas.HashBytes([]byte("gamma"))
	for _, put := range []struct {
		h  cas.Hash
		in string
	}{
		{h1, "alpha"},
		{h2, "a-longer-beta-payload"},
		{h3, "gamma"},
	} {
		if err := b.Put(ctx, put.h, strings.NewReader(put.in)); err != nil {
			t.Fatalf("Put %s = %v", put.h, err)
		}
	}

	st, err := b.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats = %v", err)
	}
	if st.ObjectCount != 3 {
		t.Errorf("ObjectCount = %d, want 3", st.ObjectCount)
	}
	if want := int64(len("alpha") + len("a-longer-beta-payload") + len("gamma")); st.TotalSize != want {
		t.Errorf("TotalSize = %d, want %d", st.TotalSize, want)
	}
	if len(st.AlgorithmCounts) != 1 {
		t.Errorf("AlgorithmCounts covers %d algorithms, want 1", len(st.AlgorithmCounts))
	}
	if got := st.AlgorithmCounts["sha256"]; got != 3 {
		t.Errorf("sha256 count = %d, want 3", got)
	}

	// Delete updates Stats.
	if err := b.Delete(ctx, h1); err != nil {
		t.Fatal(err)
	}
	st, _ = b.Stats(ctx)
	if st.ObjectCount != 2 {
		t.Errorf("ObjectCount after delete = %d, want 2", st.ObjectCount)
	}
	if want := int64(len("a-longer-beta-payload") + len("gamma")); st.TotalSize != want {
		t.Errorf("TotalSize after delete = %d, want %d", st.TotalSize, want)
	}
	if got := st.AlgorithmCounts["sha256"]; got != 2 {
		t.Errorf("sha256 count after delete = %d, want 2", got)
	}
}

// TestMemoryBackendStatsCanceled checks Stats honors a canceled context.
func TestMemoryBackendStatsCanceled(t *testing.T) {
	b := New()
	h := cas.HashBytes([]byte("alpha"))
	if err := b.Put(context.Background(), h, strings.NewReader("alpha")); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Stats(cctx); err == nil {
		t.Fatal("Stats with canceled context must error")
	}
}

func TestMemoryBackendUnboundedDefault(t *testing.T) {
	ctx := context.Background()
	b := New() // 0 = unbounded
	var h cas.Hash
	for i := 0; i < 100; i++ {
		payload := strings.Repeat("x", 1024)
		nh := cas.HashBytes([]byte(payload))
		if err := b.Put(ctx, nh, strings.NewReader(payload)); err != nil {
			t.Fatalf("Put[%d] on unbounded backend = %v", i, err)
		}
		h = nh
	}
	if ok, _ := b.Exists(ctx, h); !ok {
		t.Fatal("last object should exist")
	}
}
