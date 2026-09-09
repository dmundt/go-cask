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
	h, _ := cas.HashBytes("sha256", []byte("hello"))
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
	h, _ := cas.HashBytes("sha256", []byte("missing"))
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
	h1, _ := cas.HashBytes("sha256", []byte("alpha"))
	h2, _ := cas.HashBytes("sha256", []byte("beta"))

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
	missing, _ := cas.HashBytes("sha256", []byte("missing"))
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
