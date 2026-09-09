package prefetch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	mem "github.com/dmundt/go-cask/cas/cache/mem"
	"github.com/dmundt/go-cask/cas/cache/prefetch"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type testObject struct {
	Name string
	Refs []cas.Hash
}

func (testObject) Type() string             { return "test@1" }
func (o testObject) References() []cas.Hash { return o.Refs }

func newStore(t *testing.T) (*cas.Store[testObject], *mem.CachedStore[testObject]) {
	t.Helper()
	s, err := cas.New(backmem.New(), jsoncodec.New[testObject](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s, mem.New(s)
}

func TestSmartCache(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 2)
	h, _ := s.Put(ctx, testObject{Name: "root"})
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "root" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheZeroDepth(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 0)
	h, _ := s.Put(ctx, testObject{Name: "only"})
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "only" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheMissing(t *testing.T) {
	ctx := context.Background()
	_, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 2)
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := sc.GetWithPrefetch(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}
