// Package main implements the artifact-cache example: a content-addressed
// build-artifact cache with a custom gzip codec, a custom registered hash
// algorithm (sha256double), bounded LRU caching with a monitor, and
// mark-and-sweep GC from manifests (examples spec §3.2).
//
// Usage:
//
//	go run ./examples/artifact-cache [-store <dir>] <command> [args]
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
	"github.com/dmundt/go-cask/cas/extra"
)

const usage = `usage: artifact-cache [-store <dir>] <command> [args]

commands:
  put <name> <file>    store an artifact under name (dedup-aware); updates the manifest
  get <hash>           retrieve an artifact by hash (via the LRU cache)
  gc                   mark-and-sweep: delete artifacts no manifest references
  stats                print store statistics
  monitor <hash...>    warm the cache and print cache metrics`

// Artifact is a cached build output. Its address is the sha256double hash
// of its gzip-compressed envelope, so identical bytes always deduplicate.
type Artifact struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func (a *Artifact) Type() string { return "artifact@1" }

func (a *Artifact) References() []cas.Hash { return nil }

func (a *Artifact) Serialize() ([]byte, error) {
	return marshalEnvelope(a.Type(), gzipJSON(a))
}

func (a *Artifact) Deserialize(data []byte) (*Artifact, error) {
	payload, err := envelopePayload(data)
	if err != nil {
		return nil, err
	}
	return gunzipJSON[Artifact](payload)
}

// Manifest names the current artifact(s) of a build target. GC keeps
// everything reachable from manifests and reclaims replaced artifacts.
type Manifest struct {
	Name      string     `json:"name"`
	Artifacts []cas.Hash `json:"artifacts"`
}

func (m *Manifest) Type() string { return "manifest@1" }

func (m *Manifest) References() []cas.Hash { return m.Artifacts }

// MarshalJSON renders artifact hashes as "algo:hex" strings.
func (m Manifest) MarshalJSON() ([]byte, error) {
	arts := make([]string, 0, len(m.Artifacts))
	for _, h := range m.Artifacts {
		arts = append(arts, h.String())
	}
	return json.Marshal(struct {
		Name      string   `json:"name"`
		Artifacts []string `json:"artifacts,omitempty"`
	}{m.Name, arts})
}

// UnmarshalJSON parses the string hashes back into Hash values.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string   `json:"name"`
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Name = raw.Name
	for _, s := range raw.Artifacts {
		h, err := cas.ParseHash(s)
		if err != nil {
			return err
		}
		m.Artifacts = append(m.Artifacts, h)
	}
	return nil
}

func (m *Manifest) Serialize() ([]byte, error) {
	return marshalEnvelope(m.Type(), gzipJSON(m))
}

func (m *Manifest) Deserialize(data []byte) (*Manifest, error) {
	payload, err := envelopePayload(data)
	if err != nil {
		return nil, err
	}
	return gunzipJSON[Manifest](payload)
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
	raw       *cas.FSRawStore
	artifacts *cas.Store[*Artifact]
	manifests *cas.Store[*Manifest]
	cache     *cas.LRUCache[*Artifact]
	monitor   *extra.CacheMonitor[*Artifact]
}

func newApp(dir string) (*app, error) {
	raw, err := cas.NewFSRawStore(dir)
	if err != nil {
		return nil, err
	}
	artifacts, err := cas.NewStore(raw, newGzipCodec[*Artifact](), "sha256double")
	if err != nil {
		return nil, err
	}
	manifests, err := cas.NewStore(raw, newGzipCodec[*Manifest](), "sha256double")
	if err != nil {
		return nil, err
	}
	cache, err := cas.NewLRUCache(artifacts, 100)
	if err != nil {
		return nil, err
	}
	a := &app{raw: raw, artifacts: artifacts, manifests: manifests, cache: cache}
	a.monitor = extra.NewCacheMonitor(cache.CachedStore, 2*time.Second, func(s extra.CacheSnapshot) {
		fmt.Printf("cache: hits=%d misses=%d hit-rate=%.2f size=%d\n", s.Hits, s.Misses, s.HitRate, s.Size)
	})
	return a, nil
}

func (a *app) close() { a.monitor.Stop() }

// put stores an artifact under name and points the name's manifest at it.
// The previous manifest for the name is deleted, so the artifact it
// referenced becomes unreferenced — GC reclaims it (a build cache keeps only
// the current artifact of each target).
func (a *app) put(ctx context.Context, name, file string) (cas.Hash, bool, error) {
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
	if _, _, err := a.manifests.PutDedup(ctx, &Manifest{Name: name, Artifacts: []cas.Hash{h}}); err != nil {
		return nil, false, err
	}
	return h, dedup, nil
}

// manifestsNamed returns the stored manifest hashes for name.
func (a *app) manifestsNamed(ctx context.Context, name string) ([]cas.Hash, error) {
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []cas.Hash
	for _, h := range hashes {
		m, err := a.manifests.GetTyped(ctx, h)
		if err != nil {
			continue // not a manifest
		}
		if m.Name == name {
			out = append(out, h)
		}
	}
	return out, nil
}

// get serves an artifact by hash through the LRU cache.
func (a *app) get(ctx context.Context, h cas.Hash) (*Artifact, error) {
	obj, err := a.cache.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	return obj.(*Artifact), nil
}

// gc deletes every object not reachable from the manifests: manifest hashes
// plus the artifacts they reference.
func (a *app) gc(ctx context.Context) (int, error) {
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		return 0, err
	}
	reachable := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		m, err := a.manifests.GetTyped(ctx, h)
		if err != nil {
			continue // not a manifest (an artifact)
		}
		reachable[h.String()] = true
		for _, ref := range m.References() {
			reachable[ref.String()] = true
		}
	}
	before := len(hashes)
	if err := a.raw.GC(ctx, reachable); err != nil {
		return 0, err
	}
	return before - len(reachable), nil
}

func main() {
	ctx := context.Background()
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	dir := "./objects"
	if args[0] == "-store" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		dir, args = args[1], args[2:]
	}
	a, err := newApp(dir)
	if err != nil {
		fatal(err)
	}
	defer a.close()

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "put":
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		h, dedup, err := a.put(ctx, rest[0], rest[1])
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s deduplicated: %v\n", h, dedup)
	case "get":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		h, err := cas.ParseHash(rest[0])
		if err != nil {
			fatal(err)
		}
		art, err := a.get(ctx, h)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s (%d bytes)\n", art.Name, len(art.Data))
	case "gc":
		n, err := a.gc(ctx)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("gc: deleted %d unreachable objects\n", n)
	case "stats":
		st, err := a.raw.Stats(ctx)
		if err != nil {
			fatal(err)
		}
		fmt.Println(st)
	case "monitor":
		for _, s := range rest {
			h, err := cas.ParseHash(s)
			if err != nil {
				fatal(err)
			}
			if _, err := a.get(ctx, h); err != nil {
				fatal(err)
			}
		}
		st := a.cache.CacheStats()
		fmt.Printf("final: hits=%d misses=%d hit-rate=%.2f size=%d\n", st.Hits, st.Misses, st.HitRate, st.Size)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", cmd, usage)
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
