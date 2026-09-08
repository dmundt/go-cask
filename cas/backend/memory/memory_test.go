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
