package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache"
)

func TestSmartCache(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 2)
	h := put(t, s, "root")
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
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 0)
	h := put(t, s, "only")
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
	cs := cache.NewCachedStore(newStore(t))
	sc := cache.NewSmartCache(cs, 2)
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := sc.GetWithPrefetch(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

func TestSmartCachePrefetchGoroutine(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 1)
	h := put(t, s, "root")
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "root" {
		t.Fatalf("got %q", obj.Name)
	}
}
