package cas_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/internal/test"
)

type backendFactory func(t *testing.T) cas.Backend

func fsFactory(t *testing.T) cas.Backend {
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memFactory(t *testing.T) cas.Backend { return mem.New() }

func testBackendContract(t *testing.T, raw cas.Backend) {
	ctx := context.Background()
	h, _ := test.HashData("sha256", []byte("contract"))
	if err := raw.Put(ctx, h, strings.NewReader("contract")); err != nil {
		t.Fatal(err)
	}
	rc, err := raw.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := test.ReadAllAndClose(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "contract" {
		t.Fatalf("got %q", got)
	}
}

func mustFS(t *testing.T, opts ...backend.Option) *fs.Backend {
	s, err := fs.New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestStore(t *testing.T, raw cas.Backend) *cas.Store[test.Note] {
	t.Helper()
	s, err := cas.New(raw, jsoncodec.New[test.Note](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
