// Package main implements the artifacts example: a content-addressable
// build-artifact cache with a custom gzip codec, bounded LRU caching with a
// monitor, and mark-and-sweep GC from manifests (examples spec §3.2).
//
// Usage:
//
//	go run ./examples/artifacts [-store <dir>] <command> [args]
//
// Commands: put <name> <file>, get <hash>, gc, stats, monitor <hash...>.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	lru "github.com/dmundt/go-cask/cas/cache/lru"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

const usage = `usage: artifacts [-store <dir>] <command> [args]

commands:
  put <name> <file>    store an artifact under name (dedup-aware); updates the manifest
  get <hash>           retrieve an artifact by hash (via the LRU cache)
  gc                   mark-and-sweep: delete artifacts no manifest references
  stats                print store statistics
  monitor <hash...>    warm the cache and print cache metrics`

// Artifact is a cached build output. Its address is the hash of its
// gzip-compressed envelope, so identical bytes always deduplicate.
type Artifact struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func (a *Artifact) Type() string { return "artifact@1" }

func (a *Artifact) References() []cas.Digest { return nil }

// Manifest names the current artifact(s) of a build target. GC keeps
// everything reachable from manifests and reclaims replaced artifacts. A
// reference field is a cas.Digest: it renders itself as one hex string through
// encoding.TextMarshaler and needs no JSON code here (cas-core §4.2).
type Manifest struct {
	Name      string       `json:"name"`
	Artifacts []cas.Digest `json:"artifacts,omitempty"`
}

func (m *Manifest) Type() string { return "manifest@1" }

// References returns the artifact digests, or nil for an empty manifest (nil
// means "no references", which is what GC reads as unreachable).
func (m *Manifest) References() []cas.Digest {
	if len(m.Artifacts) == 0 {
		return nil
	}
	refs := make([]cas.Digest, 0, len(m.Artifacts))
	for _, d := range m.Artifacts {
		if !d.IsZero() {
			refs = append(refs, d)
		}
	}
	return refs
}

// gzipJSON compresses the JSON encoding of v (deterministic output: fixed
// mtime, so identical values produce identical bytes → identical hashes).
func gzipJSON(v any) []byte {
	inner, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs; cannot fail
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Header.ModTime = time.Unix(0, 0)
	if _, err := zw.Write(inner); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// gunzipJSON decompresses and decodes v.
func gunzipJSON[T any](data []byte) (*T, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	inner, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	var v T
	if err := json.Unmarshal(inner, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// app bundles the store, typed stores, and the LRU cache.
type app struct {
	raw       *fs.Backend
	artifacts *cas.Store[*Artifact]
	manifests *cas.Store[*Manifest]
	cache     *lru.Cache[*Artifact]
	monitor   *CacheMonitor[*Artifact]
}

func newApp(dir string) (*app, error) {
	raw, err := fs.New(dir)
	if err != nil {
		return nil, err
	}
	artifacts := cas.New(raw, newGzipCodec[*Artifact](), sha256.New())
	manifests := cas.New(raw, newGzipCodec[*Manifest](), sha256.New())
	cache, err := lru.New(artifacts, 100)
	if err != nil {
		return nil, err
	}
	a := &app{raw: raw, artifacts: artifacts, manifests: manifests, cache: cache}
	a.monitor = NewCacheMonitor(cache.CachedStore, 2*time.Second, func(s CacheSnapshot) {
		fmt.Printf("cache: hits=%d misses=%d hit-rate=%.2f size=%d\n", s.Hits, s.Misses, s.HitRate, s.Size)
	})
	return a, nil
}

func (a *app) close() { a.monitor.Stop() }

// put stores an artifact under name and points the name's manifest at it.
// The previous manifest for the name is deleted, so the artifact it
// referenced becomes unreferenced — GC reclaims it (a build cache keeps only
// the current artifact of each target).
func (a *app) put(ctx context.Context, name, file string) (cas.Digest, bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, false, err
	}
	h, dedup, err := a.artifacts.PutDedup(ctx, &Artifact{Name: name, Data: data})
	if err != nil {
		return nil, false, err
	}
	prev, err := a.manifestsNamed(ctx, name)
	if err != nil {
		return nil, false, err
	}
	for _, ph := range prev {
		if err := a.raw.Delete(ctx, ph); err != nil {
			return nil, false, err
		}
	}
	if _, _, err := a.manifests.PutDedup(ctx, &Manifest{Name: name, Artifacts: []cas.Digest{h}}); err != nil {
		return nil, false, err
	}
	return h, dedup, nil
}

// manifestsNamed returns the stored manifest digests for name.
func (a *app) manifestsNamed(ctx context.Context, name string) ([]cas.Digest, error) {
	digests, err := a.raw.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []cas.Digest
	for _, h := range digests {
		m, err := a.manifests.Get(ctx, h)
		if err != nil {
			continue // not a manifest
		}
		if m.Name == name {
			out = append(out, h)
		}
	}
	return out, nil
}

// get serves an artifact by digest through the LRU cache.
func (a *app) get(ctx context.Context, d cas.Digest) (*Artifact, error) {
	return a.cache.Get(ctx, d)
}

// gc deletes every object not reachable from the manifests: manifest digests
// plus the artifacts they reference.
func (a *app) gc(ctx context.Context) (int, error) {
	digests, err := a.raw.List(ctx)
	if err != nil {
		return 0, err
	}
	reachable := make(map[string]bool, len(digests))
	for _, h := range digests {
		m, err := a.manifests.Get(ctx, h)
		if err != nil {
			continue // not a manifest (an artifact)
		}
		reachable[h.String()] = true
		for _, ref := range m.References() {
			reachable[ref.String()] = true
		}
	}
	before := len(digests)
	if err := a.raw.GC(ctx, reachable); err != nil {
		return 0, err
	}
	return before - len(reachable), nil
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	dir := "./objects"
	if args[0] == "-store" {
		if len(args) < 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		dir, args = args[1], args[2:]
	}
	if len(args) < 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	a, err := newApp(dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer a.close()

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "put":
		if len(rest) != 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		h, dedup, err := a.put(ctx, rest[0], rest[1])
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s deduplicated: %v\n", h, dedup)
	case "get":
		if len(rest) != 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		h, err := sha256.Parse(rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		art, err := a.get(ctx, h)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s (%d bytes)\n", art.Name, len(art.Data))
	case "gc":
		n, err := a.gc(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "gc: deleted %d unreachable objects\n", n)
	case "stats":
		st, err := a.raw.Stats(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, st)
	case "monitor":
		for _, s := range rest {
			h, err := sha256.Parse(s)
			if err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return 1
			}
			if _, err := a.get(ctx, h); err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return 1
			}
		}
		st := a.cache.CacheStats()
		fmt.Fprintf(stdout, "final: hits=%d misses=%d hit-rate=%.2f size=%d\n", st.Hits, st.Misses, st.HitRate, st.Size)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", cmd, usage)
		return 2
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
