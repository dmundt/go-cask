// Command files is a miniature Git built on the gitlike reference object model:
// it stores file trees as content-addressable objects, commits them,
// and can log, cat, graph, verify and summarize the store — the closest
// thing to a tiny Git on CASK (examples spec §3.1).
//
// It demonstrates: gitlike Blob/Tree/Commit/Tag, Repository,
// Resolver/ResolvedObject, WalkGraph, Store[T] with the JSON codec
// (json.New[T]()), fs.Backend fan-out, Verify, Stats, derived object-state
// audit (verified/orphaned/corrupt), and a argument-parsing CLI.
//
// Usage:
//
//	go run ./examples/files [-store <dir>] <command> [args]
//
// Commands: add <file...>, commit -m <msg>, log, cat <hash>, graph,
// audit [-no-verify], verify, stats.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/gitlike"
)

const usage = `usage: files [-store <dir>] <command> [args]

commands:
  add <file...>     store files as blobs and build a tree (prints the tree hash)
  commit -m <msg>   create a commit pointing at the current tree
  log               list commits from HEAD backwards
  cat <hash>        print a blob's bytes to stdout
  graph             print the object graph reachable from HEAD
  audit [-no-verify]  report every object's state (verified/orphaned/corrupt)
  verify            recompute every stored digest
  stats             print the object count and total size`

// app bundles the store, repository and the small ref files (HEAD/INDEX).
type app struct {
	raw   *fs.Backend
	repo  *gitlike.Repository
	dir   string
	index string // path of the INDEX file (current tree)
	head  string // path of the HEAD file (current commit)
}

func newApp(dir string) (*app, error) {
	raw, err := fs.New(dir)
	if err != nil {
		return nil, err
	}
	// gitlike names neither the hash algorithm nor the wire format, so the
	// example supplies both: the sha256 hasher and one JSON codec per type.
	repo := gitlike.NewRepository(raw, sha256.New(), gitlike.Codecs{
		Blob:   jsoncodec.New[*gitlike.Blob](),
		Tree:   jsoncodec.New[*gitlike.Tree](),
		Commit: jsoncodec.New[*gitlike.Commit](),
		Tag:    jsoncodec.New[*gitlike.Tag](),
	})
	return &app{raw: raw, repo: repo, dir: dir, index: filepath.Join(dir, "INDEX"), head: filepath.Join(dir, "HEAD")}, nil
}

// readRef reads a ref file: the printable "sha256:hexdigest" form (or bare
// hex).
func (a *app) readRef(path string) (cas.Digest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return sha256.Parse(strings.TrimSpace(string(b)))
}

func (a *app) writeRef(path string, d cas.Digest) error {
	return os.WriteFile(path, []byte(sha256.Format(d)+"\n"), 0o644)
}

func (a *app) currentTree() (cas.Digest, error) { return a.readRef(a.index) }

func (a *app) headCommit() (cas.Digest, error) { return a.readRef(a.head) }

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
	if err := a.writeRef(a.index, h); err != nil {
		return nil, err
	}
	return h, nil
}

// commit creates a Commit pointing at the current tree, with the previous
// head as parent (if any), and advances HEAD.
func (a *app) commit(ctx context.Context, msg string) (cas.Digest, error) {
	tree, err := a.currentTree()
	if err != nil {
		return nil, fmt.Errorf("no tree to commit (run add first): %w", err)
	}
	parent, _ := a.headCommit() // absent for the first commit
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
	return h, a.writeRef(a.head, h)
}

// log walks the commit chain from HEAD backwards (parents only).
func (a *app) log(ctx context.Context, out io.Writer) error {
	h, err := a.headCommit()
	if err != nil {
		return fmt.Errorf("no commits yet: %w", err)
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

// verify recomputes every stored digest and reports any corruption.
func (a *app) verify(ctx context.Context) error {
	digests, err := a.raw.List(ctx)
	if err != nil {
		return err
	}
	bad := 0
	for _, h := range digests {
		if err := a.raw.Verify(ctx, h, sha256.New()); err != nil {
			fmt.Fprintf(os.Stderr, "CORRUPT %s: %v\n", h, err)
			bad++
		}
	}
	fmt.Printf("verified %d objects, %d corrupt\n", len(digests), bad)
	if bad > 0 {
		return fmt.Errorf("%d corrupt objects", bad)
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
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
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
		if len(args) < 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
	}
	a, err := newApp(dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	cmd, rest := args[0], args[1:]
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
		h, err := a.headCommit()
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
		st, err := a.raw.Stats(ctx)
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
