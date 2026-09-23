// Package main implements the artifacts example: a content-addressable
// build-artifact cache with the shipped gzip codec wrapper, bounded LRU caching
// with a monitor, named manifest refs, and mark-and-sweep GC from those refs
// (examples spec §3.2).
//
// Usage:
//
//	go run ./examples/artifacts [-store <root>] <command> [args]
//
// Commands: put <name> <file>, get <name|hash>, gc, stats, monitor <hash...>.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	lru "github.com/dmundt/go-cask/cas/cache/lru"
	gzipcodec "github.com/dmundt/go-cask/cas/codec/gzip"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
)

const usage = `usage: artifacts [-store <root>] <command> [args]

-store is the example root. It holds two independent trees: objects/ (the
fs.Backend base) and refs/ (one small pointer file per manifest name).

commands:
  put <name> <file>    store an artifact under name (dedup-aware); moves the name's
                       ref to the new manifest and deletes the manifest it replaced
  get <name|hash>      retrieve an artifact by ref name or hash (via the LRU cache)
  gc                   mark-and-sweep: delete objects no named manifest reaches;
                       aborts, deleting nothing, if a manifest cannot be decoded
  stats                print store statistics
  monitor <hash...>    warm the cache and print cache metrics`

// The versioned type names the two stores write and read back. gc reads them
// back with Store.Type to tell the two apart: a digest the manifest store did
// not write is an artifact, and only a manifest@1 has references to expand.
const (
	artifactType = "artifact@1"
	manifestType = "manifest@1"
)

// Artifact is a cached build output. Its address is the hash of its
// gzip-compressed envelope, so identical bytes always deduplicate.
type Artifact struct {
	// Name identifies the artifact.
	Name string `json:"name"`
	// Data contains the artifact bytes.
	Data []byte `json:"data"`
}

// Type returns the versioned artifact type name.
func (a *Artifact) Type() string { return artifactType }

// References returns nil because artifacts are leaves.
func (a *Artifact) References() []cas.Digest { return nil }

// Manifest names the current artifact(s) of a build target. A manifest is not
// found by scanning the store: the target's name is a ref (cas/refs) pointing
// at the manifest digest, and GC keeps everything reachable from the refs. A
// reference field is a cas.Digest: it renders itself as one hex string through
// encoding.TextMarshaler and needs no JSON code here (cas-core §4.2).
type Manifest struct {
	// Name identifies the manifest.
	Name string `json:"name"`
	// Artifacts lists referenced artifact digests.
	Artifacts []cas.Digest `json:"artifacts,omitempty"`
}

// Type returns the versioned manifest type name.
func (m *Manifest) Type() string { return manifestType }

// References returns the artifact digests, or nil for an empty manifest (nil
// means "no references", which is what GC reads as unreachable).
func (m *Manifest) References() []cas.Digest {
	if len(m.Artifacts) == 0 {
		return nil
	}
	digests := make([]cas.Digest, 0, len(m.Artifacts))
	for _, d := range m.Artifacts {
		if !d.IsZero() {
			digests = append(digests, d)
		}
	}
	return digests
}

// app bundles the store, typed stores, the named refs, and the LRU cache.
type app struct {
	backend   *fs.Backend
	artifacts *cas.Store[*Artifact]
	manifests *cas.Store[*Manifest]
	refs      *refs.Store
	cache     *lru.Cache[*Artifact]
	monitor   *CacheMonitor[*Artifact]
}

// newApp builds the example over one root directory, which owns two
// independent trees: <root>/objects is the fs.Backend base and <root>/refs is
// the refs.Store directory.
//
// Refs MUST live outside the objects base (cas-core §4.4, one base = one
// store). refs.Store writes "<name>.tmp" temp files next to each ref, and
// fs.Backend.Clean reclaims every "*.tmp" file beneath its base, so refs
// written inside the base would have their in-flight temp files removed under
// them; and List/Stats report any digest-named file beneath the base, so a ref
// file that happened to be named like a digest would be counted as — and swept
// as — an object.
func newApp(root string) (*app, error) {
	backend, err := fs.New(filepath.Join(root, "objects"))
	if err != nil {
		return nil, err
	}
	refStore, err := refs.Open(filepath.Join(root, "refs"))
	if err != nil {
		return nil, err
	}
	// The composition is explicit at the call site: the JSON codec serializes,
	// the shipped gzip codec wraps it (cas-core §7.2, §4.6). The wrapper names
	// itself ("gzip+json"), so the envelope records the wire format and a codec
	// change reads as ErrCodecMismatch rather than a decode failure, and it
	// bounds decompression (cas/codec/gzip.MaxDecodedBytes).
	artifacts := cas.New(backend, gzipcodec.New(jsoncodec.New[*Artifact]()), sha256.New())
	manifests := cas.New(backend, gzipcodec.New(jsoncodec.New[*Manifest]()), sha256.New())
	cache, err := lru.New(artifacts, 100)
	if err != nil {
		return nil, err
	}
	a := &app{backend: backend, artifacts: artifacts, manifests: manifests, refs: refStore, cache: cache}
	a.monitor = NewCacheMonitor(cache.CachedStore(), 2*time.Second, func(s CacheSnapshot) {
		fmt.Printf("cache: hits=%d misses=%d hit-rate=%.2f size=%d\n", s.Hits, s.Misses, s.HitRate, s.Size)
	})
	return a, nil
}

func (a *app) close() { a.monitor.Stop() }

// put stores an artifact under name and points the name's ref at the manifest
// that references it. The name's previous manifest digest is read from the ref
// first — one small file, not a scan of the store — and the manifest it named
// is deleted once the new pointer is published, so the replaced manifest
// becomes garbage exactly as before (a build cache keeps only the current
// artifact of each target; GC reclaims what the update orphaned).
func (a *app) put(ctx context.Context, name, file string) (cas.Digest, bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, false, err
	}
	// Read the ref before writing anything: it is also what validates the
	// name, so an unusable name stores no object at all.
	prev, err := a.refs.Get(ctx, name)
	if err != nil && !errors.Is(err, refs.ErrNotFound) {
		return nil, false, fmt.Errorf("put: read ref %q: %w", name, err)
	}
	h, dedup, err := a.artifacts.PutDedup(ctx, &Artifact{Name: name, Data: data})
	if err != nil {
		return nil, false, err
	}
	mh, _, err := a.manifests.PutDedup(ctx, &Manifest{Name: name, Artifacts: []cas.Digest{h}})
	if err != nil {
		return nil, false, err
	}
	// Publish the new pointer before dropping the manifest it replaces: a
	// crash in between leaves the ref pointing at a live object (the old
	// manifest is then merely unreachable garbage) instead of dangling at a
	// digest that is already gone.
	if err := a.refs.Set(ctx, name, mh); err != nil {
		return nil, false, fmt.Errorf("put: set ref %q: %w", name, err)
	}
	if !prev.IsZero() && !prev.Equal(mh) {
		if err := a.backend.Delete(ctx, prev); err != nil {
			return nil, false, fmt.Errorf("put: delete replaced manifest %s: %w", prev, err)
		}
	}
	return h, dedup, nil
}

// artifactFor resolves a get argument to an artifact digest: a stored ref name
// is read through the refs store — the pointer put maintains — and yields the
// single artifact of the manifest it names; anything else must be an artifact
// digest. The ref lookup comes first, so a ref name spelled like hex still
// means the ref.
func (a *app) artifactFor(ctx context.Context, arg string) (cas.Digest, error) {
	mh, err := a.refs.Get(ctx, arg)
	switch {
	case err == nil:
		m, err := a.manifests.Get(ctx, mh)
		if err != nil {
			return nil, fmt.Errorf("read manifest %s for ref %q: %w", mh, arg, err)
		}
		referenced := m.References()
		if len(referenced) != 1 {
			return nil, fmt.Errorf("ref %q names a manifest with %d artifacts, want exactly 1", arg, len(referenced))
		}
		return referenced[0], nil
	case errors.Is(err, refs.ErrNotFound), errors.Is(err, refs.ErrInvalidName):
		// No such ref — the argument is not even a legal ref name in the
		// second case — so it must be a digest instead.
		d, err := sha256.Parse(arg)
		if err != nil {
			return nil, fmt.Errorf("resolve %q as a ref name or a digest: %w", arg, err)
		}
		return d, nil
	default:
		return nil, fmt.Errorf("read ref %q: %w", arg, err)
	}
}

// get serves an artifact by digest through the LRU cache.
func (a *app) get(ctx context.Context, d cas.Digest) (*Artifact, error) {
	return a.cache.Get(ctx, d)
}

// gc deletes every object the named manifests do not reach. The root set is the
// refs' current digests; cas.Reachable expands it through the manifest store (a
// reference lister over the core's Digest-only walk); cas.Sweep deletes what is
// left and returns exactly the digests it removed, which is the count reported
// here — not arithmetic on the reachable map.
//
// The lister is where the data-loss bug lived, so it decides "leaf" from the
// STORED TYPE, not from a failed read. Store.Type reports what an object holds
// without decoding its payload (PeekType reads the header and stops), and
// anything that is not manifest@1 references nothing this example must keep:
// artifacts are leaves, and artifacts are the only other type the store holds.
// Every other outcome aborts gc before anything is deleted — an envelope whose
// header cannot be read (Store.Type reports ErrCorrupt), a manifest@1 whose
// payload cannot be decoded, a digest that is not there at all. A manifest that
// is present and undecodable still references its artifacts; treating it as a
// leaf is exactly how those artifacts used to be swept away.
//
// Store.Get's ErrUnknownType cannot make that call on its own: Get returns the
// same sentinel both for "stored type is not manifest@1" and for a malformed
// envelope (cas/store.go, cas/envelope.go decodeEnvelopeHeader), so deciding
// "leaf" from it would let a truncated manifest be mistaken for an artifact.
func (a *app) gc(ctx context.Context) (int, error) {
	roots, err := a.refs.Roots(ctx)
	if err != nil {
		return 0, fmt.Errorf("gc: read refs: %w", err)
	}
	resolve := cas.RefListerFunc(func(ctx context.Context, d cas.Digest) ([]cas.Digest, error) {
		typeName, err := a.manifests.Type(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("read stored type of %s: %w", d, err)
		}
		if typeName != manifestType {
			return nil, nil // an artifact: a leaf, it references nothing
		}
		m, err := a.manifests.Get(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("decode manifest %s: %w", d, err)
		}
		return m.References(), nil
	})
	reachable, err := cas.Reachable(ctx, resolve, roots)
	if err != nil {
		return 0, fmt.Errorf("gc: mark: %w", err)
	}
	doomed, err := cas.Sweep(ctx, a.backend, reachable, cas.SweepOptions{})
	if err != nil {
		return 0, fmt.Errorf("gc: sweep: %w", err)
	}
	return len(doomed), nil
}

// run executes the CLI and returns the process exit code: 0 on success, 1 on a
// runtime error, 2 on a usage error (cli.md §3).
//
// The optional -store flag is parsed by the standard flag package, so
// -store=dir, --store dir, -h and an unknown flag all behave as they do in any
// Go CLI. Parsing stops at the subcommand, which owns the remaining arguments.
func run(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	flags := flag.NewFlagSet("artifacts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("store", "./store", "example root (holds objects/ and refs/)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		// flag has already reported the problem — or printed the usage for
		// -h/-help — to stderr, so only the exit code is left to set.
		return 2
	}
	rest := flags.Args()
	if len(rest) < 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	a, err := newApp(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer a.close()

	cmd, rest := rest[0], rest[1:]
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
		h, err := a.artifactFor(ctx, rest[0])
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
		st, err := a.backend.Stats(ctx)
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
