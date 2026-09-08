package lru_test

import (
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/cache/lru"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type item struct {
	ID string
}

func (item) Type() string           { return "item@1" }
func (item) References() []cas.Hash { return nil }

func newStore(t *testing.T) *cas.Store[item] {
	t.Helper()
	s, err := cas.New(mem.New(), jsoncodec.New[item](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func putItem(t *testing.T, s *cas.Store[item], id string) cas.Hash {
	t.Helper()
	h, err := s.Put(context.Background(), item{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCache(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 3; i++ {
		hs = append(hs, putItem(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := c.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("size = %d, want 2", st.Size)
	}
	if st := c.CacheStats(); st.Evicts != 1 {
		t.Fatalf("evicts = %d, want 1", st.Evicts)
	}
}

func TestNewRejectsBadSize(t *testing.T) {
	s := newStore(t)
	if _, err := lru.New(s, 0); err == nil {
		t.Fatal("maxSize 0 must be rejected")
	}
	if _, err := lru.New(s, -1); err == nil {
		t.Fatal("negative maxSize must be rejected")
	}
}

func TestCacheBoundViaAllAccessors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 5; i++ {
		hs = append(hs, putItem(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := c.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.CacheStats(); st.Size > 2 {
		t.Fatalf("Get exceeded bound: size = %d", st.Size)
	}
}
