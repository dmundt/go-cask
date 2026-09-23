// Command files is a miniature Git built on the gitlike reference object model:
// it stores file trees as content-addressable objects, commits them,
// and can log, cat, graph, verify and summarize the store — the closest
// thing to a tiny Git on CASK (examples spec §3.1).
//
// It demonstrates: gitlike Blob/Tree/Commit/Tag, Repository,
// Resolver/ResolvedObject, WalkGraph, Store[T] with the JSON codec
// (json.New[T]()), fs.Backend fan-out, cas/refs for the mutable HEAD/INDEX
// pointers, explicit cas.Verifier/cas.VerifyAll integrity checks, Stats,
// derived object-state audit (verified/orphaned/corrupt), and an
// argument-parsing CLI.
//
// Usage:
//
//	go run ./examples/files [-store <root>] <command> [args]
//
// Commands: add <file...>, commit -m <msg>, log, cat <hash>, graph,
// audit [-no-verify], verify, stats.
//
// The -store root holds two independent trees — the Git shape this example
// imitates, and the only shape that is safe for both:
//
//	<root>/objects  the fs.Backend base: every content-addressable object
//	<root>/refs     the cas/refs.Store: HEAD (current commit), INDEX (current tree)
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
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
	"github.com/dmundt/go-cask/gitlike"
)

// The two refs this example keeps: HEAD names the current commit, INDEX the
// tree a commit would record next. cas/refs validates the names and stores
// bare-hex digests.
const (
	headRef  = "HEAD"
	indexRef = "INDEX"
)

const usage = `usage: files [-store <root>] <command> [args]

commands:
  add <file...>     store files as blobs and build a tree (prints the tree hash)
  commit -m <msg>   create a commit pointing at the current tree
  log               list commits from HEAD backwards
  cat <hash>        print a blob's bytes to stdout
  graph             print the object graph reachable from HEAD
  audit [-no-verify]  report every object's state (verified/orphaned/corrupt)
  verify            recompute every stored digest
  stats             print the object count and total size

The -store root holds objects/ (the object store) and refs/ (HEAD and INDEX).`

// app bundles the object store, the gitlike repository over it, the mutable
// refs, and the hasher each integrity check recomputes with.
//
// objects and refs are separate directories under root, and that separation is
// load-bearing rather than cosmetic: the refs Store publishes a new value
// through a "<name>.tmp" temp file, and the fs backend's Clean reclaims every
// "*.tmp" beneath its own base — so a ref written inside <root>/objects could
// be swept away mid-write. The same base reports every digest-named file
// beneath it at any depth as an object (cas-core §4.4: one base belongs to
// exactly one store), so refs must not live there either.
type app struct {
	backend *fs.Backend
	repo    *gitlike.Repository
	hasher  cas.Hasher
	root    string      // the example root the CLI was pointed at
	objects string      // <root>/objects: the fs.Backend base
	refs    *refs.Store // <root>/refs: HEAD and INDEX
}

func newApp(root string) (*app, error) {
	objects := filepath.Join(root, "objects")
	backend, err := fs.New(objects)
	if err != nil {
		return nil, fmt.Errorf("open object store %s: %w", objects, err)
	}
	// gitlike names neither the hash algorithm nor the wire format, so the
	// example supplies both: the sha256 hasher and one JSON codec per type.
	hasher := sha256.New()
	repo := gitlike.NewRepository(backend, hasher, gitlike.Codecs{
		Blob:   jsoncodec.New[*gitlike.Blob](),
		Tree:   jsoncodec.New[*gitlike.Tree](),
		Commit: jsoncodec.New[*gitlike.Commit](),
		Tag:    jsoncodec.New[*gitlike.Tag](),
	})
	// Refs live beside the objects base, never inside it (see app's doc
	// comment): refs.Open owns <root>/refs as a plain directory of files.
	refStore, err := refs.Open(filepath.Join(root, "refs"))
	if err != nil {
		return nil, fmt.Errorf("open refs: %w", err)
	}
	return &app{
		backend: backend,
		repo:    repo,
		hasher:  hasher,
		root:    root,
		objects: objects,
		refs:    refStore,
	}, nil
}

// currentTree returns the tree INDEX points at; refs.ErrNotFound means no add
// has staged a tree yet.
func (a *app) currentTree(ctx context.Context) (cas.Digest, error) {
	return a.refs.Get(ctx, indexRef)
}

// headCommit returns the commit HEAD points at.
func (a *app) headCommit(ctx context.Context) (cas.Digest, error) {
	return a.refs.Get(ctx, headRef)
}

// headCommitOrAbsent reads HEAD and distinguishes "the store has no HEAD yet"
// from "HEAD exists but cannot be read". Only a genuinely missing ref
// (refs.ErrNotFound) is absent — the first-commit case. An unreadable or
// malformed HEAD is corruption, and callers must see it instead of silently
// treating the store as empty.
func (a *app) headCommitOrAbsent(ctx context.Context) (cas.Digest, bool, error) {
	d, err := a.headCommit(ctx)
	switch {
	case err == nil:
		return d, !d.IsZero(), nil
	case errors.Is(err, refs.ErrNotFound):
		return nil, false, nil // no HEAD ref yet: nothing committed
	default:
		return nil, false, fmt.Errorf("read HEAD: %w", err)
	}
}

// add stores each file as a blob and builds a tree of them; identical
// content deduplicates (same bytes → same digest → stored once).
func (a *app) add(ctx context.Context, paths []string) (cas.Digest, error) {
	var entries []gitlike.TreeEntry
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		h, err := a.repo.Blobs.Put(ctx, &gitlike.Blob{Data: data})
		if err != nil {
			return nil, err
		}
		entries = append(entries, gitlike.TreeEntry{Name: filepath.Base(p), Hash: h, Mode: "100644"})
	}
	h, err := a.repo.Trees.Put(ctx, &gitlike.Tree{Entries: entries})
	if err != nil {
		return nil, err
	}
	// Deliberately nothing is persisted beside the new objects (the old CRC32
	// sidecar write used to stand here): integrity is a recompute from the
	// object's own bytes (cas.Verify/cas.VerifyAll), so an object has no
	// second file that a later run would have to keep in sync.
	if err := a.refs.Set(ctx, indexRef, h); err != nil {
		return nil, err
	}
	return h, nil
}

// commit creates a Commit pointing at the current tree, with the previous
// head as parent (if any), and advances HEAD. The first commit has no parent
// because HEAD is absent, not because reading it failed.
func (a *app) commit(ctx context.Context, msg string) (cas.Digest, error) {
	tree, err := a.currentTree(ctx)
	if err != nil {
		return nil, fmt.Errorf("no tree to commit (run add first): %w", err)
	}
	parent, _, err := a.headCommitOrAbsent(ctx)
	if err != nil {
		return nil, err
	}
	c := &gitlike.Commit{
		Tree:    tree,
		Parent:  parent,
		Author:  "files",
		Message: msg,
		Time:    time.Now(),
	}
	h, err := a.repo.Commits.Put(ctx, c)
	if err != nil {
		return nil, err
	}
	return h, a.refs.Set(ctx, headRef, h)
}

// log walks the commit chain from HEAD backwards (parents only).
func (a *app) log(ctx context.Context, out io.Writer) error {
	h, present, err := a.headCommitOrAbsent(ctx)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("no commits yet")
	}
	for !h.IsZero() {
		c, err := a.repo.Commits.Get(ctx, h)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", printable(h), c.Message)
		h = c.Parent // absent for a root commit: the walk ends
	}
	return nil
}

// cat resolves d to any object and writes its bytes to out.
func (a *app) cat(ctx context.Context, d cas.Digest, out io.Writer) error {
	ro, err := gitlike.NewResolver(a.repo).ResolveAny(ctx, d)
	if err != nil {
		return err
	}
	if ro.Blob != nil {
		_, err = out.Write(ro.Blob.Data)
		return err
	}
	_, err = fmt.Fprintln(out, gitlike.PrintObject(ro))
	return err
}

// verify recomputes every stored object's digest from its bytes and reports
// any mismatch — the core's portable whole-store pass. There is no app-level
// checksum to keep in sync: an object is its own integrity record.
func (a *app) verify(ctx context.Context) error {
	report, err := cas.VerifyAll(ctx, a.backend, a.hasher)
	if err != nil {
		return err
	}
	for _, d := range report.Bad {
		fmt.Fprintf(os.Stderr, "CORRUPT %s: %v\n", d, cas.ErrDigestMismatch)
	}
	fmt.Printf("verified %d objects, %d corrupt\n", report.Checked, len(report.Bad))
	if len(report.Bad) > 0 {
		return fmt.Errorf("%d corrupt objects", len(report.Bad))
	}
	return nil
}

// printable renders a digest in the client's printable form ("sha256:hexdigest")
// for the log output, or a marker when it is absent. It is NOT a short form:
// truncation is cas.Digest.Prefix's job (the viewer's Prefix(8)).
func printable(d cas.Digest) string {
	if d.IsZero() {
		return "<absent>"
	}
	return sha256.Format(d)
}

// run executes the CLI and returns the process exit code. It is factored out
// of main so the subcommand dispatch is testable. stdout/stderr are injectable
// for tests; in production they are os.Stdout/os.Stderr.
//
// The optional -store flag is parsed by the standard flag package, so
// -store=dir, --store dir, -h and an unknown flag all behave as they do in any
// Go CLI. Parsing stops at the subcommand, which owns the remaining arguments;
// a usage error is exit 2, a runtime error exit 1 (cli.md §3).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("files", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("store", "./store", "example root directory; objects/ (the store base) and refs/ (HEAD, INDEX) are created inside it")
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
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "add":
		if len(rest) == 0 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		h, err := a.add(ctx, rest)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, h)
	case "commit":
		if len(rest) < 2 || rest[0] != "-m" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		h, err := a.commit(ctx, rest[1])
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, h)
	case "log":
		if err := a.log(ctx, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	case "cat":
		if len(rest) != 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		h, err := sha256.Parse(rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := a.cat(ctx, h, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	case "graph":
		h, err := a.headCommit(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		res := gitlike.NewResolver(a.repo)
		if err := gitlike.WalkGraph(ctx, res, h, func(ro *gitlike.ResolvedObject) error {
			fmt.Fprintf(stdout, "%-12s %s\n", ro.Type, gitlike.PrintObject(ro))
			return nil
		}); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	case "audit":
		noVerify := len(rest) == 1 && rest[0] == "-no-verify"
		if len(rest) > 1 || (len(rest) == 1 && !noVerify) {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		rep, err := a.audit(ctx, noVerify)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		rep.print(stdout)
	case "verify":
		if err := a.verify(ctx); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	case "stats":
		st, err := a.backend.Stats(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, st)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", cmd, usage)
		return 2
	}
	return 0
}

func main() {
	code := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
