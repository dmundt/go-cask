package extra_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/extra"
)

// testDoc is a leaf object used by the extra-package tests.
type testDoc struct {
	Title string `json:"title"`
}

func (d testDoc) Type() string { return "doc@1" }
func (d testDoc) References() []cas.Hash {
	return nil
}
func (d testDoc) Serialize() ([]byte, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{d.Type(), base64.StdEncoding.EncodeToString(payload)})
}
func (d testDoc) Deserialize(data []byte) (testDoc, error) {
	var env struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return testDoc{}, err
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return testDoc{}, err
	}
	var v testDoc
	if err := json.Unmarshal(payload, &v); err != nil {
		return testDoc{}, err
	}
	return v, nil
}

// testLink references another testLink by hash, so prefetching has a graph
// to warm.
type testLink struct {
	Name string   `json:"name"`
	Refs []string `json:"refs,omitempty"`
}

func (l testLink) Type() string { return "link@1" }
func (l testLink) References() []cas.Hash {
	refs := make([]cas.Hash, 0, len(l.Refs))
	for _, r := range l.Refs {
		if h, err := cas.ParseHash(r); err == nil {
			refs = append(refs, h)
		}
	}
	return refs
}
func (l testLink) Serialize() ([]byte, error) {
	payload, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{l.Type(), base64.StdEncoding.EncodeToString(payload)})
}
func (l testLink) Deserialize(data []byte) (testLink, error) {
	var env struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return testLink{}, err
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return testLink{}, err
	}
	var v testLink
	if err := json.Unmarshal(payload, &v); err != nil {
		return testLink{}, err
	}
	return v, nil
}

func TestSmartCacheGetWithPrefetch(t *testing.T) {
	ctx := context.Background()
	raw := cas.NewMemoryRawStore()
	store, err := cas.NewStore(raw, cas.JSONCodec[testLink]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	cached := cas.NewCachedStore(store)
	smart := extra.NewSmartCache(cached, 2) // preload 2 reference levels

	// Build a two-level graph: root -> mid -> leaf.
	leaf, err := store.Put(ctx, testLink{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := store.Put(ctx, testLink{Name: "mid", Refs: []string{leaf.String()}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.Put(ctx, testLink{Name: "root", Refs: []string{mid.String()}})
	if err != nil {
		t.Fatal(err)
	}

	obj, err := smart.GetWithPrefetch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Type() != "link@1" {
		t.Fatalf("Type() = %q", obj.Type())
	}

	// The prefetch is asynchronous: wait until the references are cached.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := cached.CacheStats()
		if st.Size >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := cached.CacheStats(); st.Size < 3 {
		t.Fatalf("prefetch did not warm the cache: size = %d, want >= 3", st.Size)
	}
}

func TestSmartCacheNoPrefetchDepthZero(t *testing.T) {
	ctx := context.Background()
	raw := cas.NewMemoryRawStore()
	store, err := cas.NewStore(raw, cas.JSONCodec[testLink]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	cached := cas.NewCachedStore(store)
	smart := extra.NewSmartCache(cached, 0) // prefetch disabled

	leaf, err := store.Put(ctx, testLink{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.Put(ctx, testLink{Name: "root", Refs: []string{leaf.String()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smart.GetWithPrefetch(ctx, root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // give any (wrong) prefetch time to run
	if st := cached.CacheStats(); st.Size != 1 {
		t.Fatalf("depth-0 cache size = %d, want 1", st.Size)
	}
}

func TestSmartCacheMissingObject(t *testing.T) {
	raw := cas.NewMemoryRawStore()
	store, err := cas.NewStore(raw, cas.JSONCodec[testDoc]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	smart := extra.NewSmartCache(cas.NewCachedStore(store), 1)
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := smart.GetWithPrefetch(context.Background(), missing); err == nil {
		t.Fatal("missing object must error")
	}
}

func TestCacheMonitor(t *testing.T) {
	ctx := context.Background()
	raw := cas.NewMemoryRawStore()
	store, err := cas.NewStore(raw, cas.JSONCodec[testDoc]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	cached := cas.NewCachedStore(store)

	h, err := store.Put(ctx, testDoc{Title: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var snapshots []extra.CacheSnapshot
	mon := extra.NewCacheMonitor(cached, 10*time.Millisecond, func(s extra.CacheSnapshot) {
		mu.Lock()
		snapshots = append(snapshots, s)
		mu.Unlock()
	})

	// Drive some cache traffic and wait for at least one snapshot.
	if _, err := cached.GetTyped(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetTyped(ctx, h); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(snapshots)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mon.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) == 0 {
		t.Fatal("monitor emitted no snapshots")
	}
	last := snapshots[len(snapshots)-1]
	if last.Size != 1 {
		t.Fatalf("last snapshot size = %d, want 1", last.Size)
	}
	if last.Hits < 1 {
		t.Fatalf("last snapshot hits = %d, want >= 1", last.Hits)
	}
	if last.HitRate <= 0 || last.HitRate > 1 {
		t.Fatalf("hit rate out of range: %v", last.HitRate)
	}
	// Stop is idempotent and must not hang.
	mon.Stop()
}
