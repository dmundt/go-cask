package lru_test

import (
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache/lru"
)

// TestCache_CachedStoreExposesTheSameEntriesThePolicyKept pins the observer
// accessor: CachedStore() hands back the exact wrapped store whose entries the
// LRU policy evicts, so an observer reading CacheStats or Lookup through it sees
// the post-eviction state rather than a second, independent cache.
//
// Two defensive branches of the eviction bookkeeping stay uncovered on purpose
// (testing-strategy §5):
//
//   - lru.go:77 (`break` when list.Back() is nil). The loop runs only while
//     list.Len() > maxSize, and note/touchLocked push exactly one element per
//     cached key, so a non-empty list always has a back element.
//   - lru.go:131 (`return` when a lookup under the lock finds no cached object).
//     touchLocked's only nil-co caller is note, which the wrapped store invokes
//     immediately after LoadOrStore has put the object in the map, so the
//     Lookup it falls back to can never miss.
func TestCache_CachedStoreExposesTheSameEntriesThePolicyKept(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	cache, err := lru.New(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	first := putItem(t, store, "first")
	second := putItem(t, store, "second")
	third := putItem(t, store, "third")

	// Touching them in order makes "first" the least recently used entry, so
	// inserting "third" evicts it.
	for _, d := range []cas.Digest{first, second, third} {
		if _, err := cache.Get(ctx, d); err != nil {
			t.Fatalf("Get(%s): %v", d, err)
		}
	}

	cached := cache.CachedStore()
	if cached == nil {
		t.Fatal("CachedStore() = nil, want the wrapped store")
	}
	if got := cached.Lookup(first.String()); got != nil {
		t.Fatalf("CachedStore().Lookup(evicted) = %v, want nil", got)
	}
	for _, tc := range []struct {
		name string
		d    cas.Digest
		want string
	}{
		{"second", second, "second"},
		{"third", third, "third"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := cached.Lookup(tc.d.String())
			if obj == nil {
				t.Fatalf("CachedStore().Lookup(%s) = nil, want the retained entry", tc.d)
			}
			got, err := obj.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.want {
				t.Fatalf("CachedStore().Lookup(%s).Load() = %q, want %q", tc.d, got.ID, tc.want)
			}
		})
	}
	if st := cache.CacheStats(); st.Size != 2 || st.Evicts != 1 {
		t.Fatalf("CacheStats() = size %d evicts %d, want size 2 evicts 1", st.Size, st.Evicts)
	}
}
