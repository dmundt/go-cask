// Command versioned-files is a miniature Git built on the gitlike example
// layer: it stores file trees as content-addressed objects, commits them,
// and can log, cat, graph, verify and summarize the store — the closest
// thing to a tiny Git on CASK (examples spec §3.1).
//
// It demonstrates: gitlike Blob/Tree/Commit/Tag, Repository,
// Resolver/ResolvedObject, WalkGraph, Store[T] with JSONCodec[T],
// FSRawStore fan-out, Verify, Stats, and a std-flag CLI.
//
// Usage:
//
//	go run ./examples/versioned-files [-store <dir>] <command> [args]
//
// Commands: add <file...>, commit -m <msg>, log, cat <hash>, graph,
// verify, stats.
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
	"github.com/dmundt/go-cask/examples/gitlike"
)

const usage = `usage: versioned-files [-store <dir>] <command> [args]

commands:
  add <file...>     store files as blobs and build a tree (prints the tree hash)
  commit -m <msg>   create a commit pointing at the current tree
  log               list commits from HEAD backwards
  cat <hash>        print a blob's bytes to stdout
  graph             print the object graph reachable from HEAD
  verify            recompute every stored hash
  stats             print per-algorithm counts and total size`

// app bundles the store, repository and the small ref files (HEAD/INDEX).
type app struct {
	raw   *cas.FSRawStore
	repo  *gitlike.Repository
	dir   string
	index string // path of the INDEX file (current tree)
	head  string // path of the HEAD file (current commit)
}

func newApp(dir string) (*app, error) {
	raw, err := cas.NewFSRawStore(dir)
	if err != nil {
		return nil, err
	}
	repo, err := gitlike.NewRepository(raw, "sha256")
	if err != nil {
		return nil, err
	}
	return &app{raw: raw, repo: repo, dir: dir, index: filepath.Join(dir, "INDEX"), head: filepath.Join(dir, "HEAD")}, nil
}

func (a *app) readRef(path string) (cas.Hash, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return cas.ParseHash(strings.TrimSpace(string(b)))
}

func (a *app) writeRef(path string, h cas.Hash) error {
	return os.WriteFile(path, []byte(h.String()+"\n"), 0o644)
}

func (a *app) currentTree() (cas.Hash, error) { return a.readRef(a.index) }

func (a *app) headCommit() (cas.Hash, error) { return a.readRef(a.head) }

// add stores each file as a blob and builds a tree of them; identical
// content deduplicates (same bytes → same hash → stored once).
func (a *app) add(ctx context.Context, paths []string) (cas.Hash, error) {
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
func (a *app) commit(ctx context.Context, msg string) (cas.Hash, error) {
	tree, err := a.currentTree()
	if err != nil {
		return nil, fmt.Errorf("no tree to commit (run add first): %w", err)
	}
	parent, _ := a.headCommit() // no parent for the first commit
	c := &gitlike.Commit{
		Tree:    tree,
		Parent:  parent,
		Author:  "versioned-files",
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
	for h != nil {
		c, err := a.repo.Commits.GetTyped(ctx, h)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", short(h), c.Message)
		h = c.Parent
	}
	return nil
}

// cat resolves h to a blob and writes its bytes to out.
func (a *app) cat(ctx context.Context, h cas.Hash, out io.Writer) error {
	ro, err := gitlike.NewResolver(a.repo).ResolveAny(ctx, h)
	if err != nil {
		return err
	}
	if ro.Blob == nil {
		return fmt.Errorf("%s is not a blob", h)
	}
	_, err = out.Write(ro.Blob.Data)
	return err
}

// verify recomputes every stored hash and reports any corruption.
func (a *app) verify(ctx context.Context) error {
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		return err
	}
	bad := 0
	for _, h := range hashes {
		if err := a.raw.Verify(ctx, h); err != nil {
			fmt.Fprintf(os.Stderr, "CORRUPT %s: %v\n", h, err)
			bad++
		}
	}
	fmt.Printf("verified %d objects, %d corrupt\n", len(hashes), bad)
	if bad > 0 {
		return fmt.Errorf("%d corrupt objects", bad)
	}
	return nil
}

func short(h cas.Hash) string {
	if h == nil {
		return "<nil>"
	}
	return h.String()
}

func main() {
	ctx := context.Background()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	dir := "./objects"
	args := os.Args[1:]
	if args[0] == "-store" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		dir, args = args[1], args[2:]
	}
	a, err := newApp(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "add":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		h, err := a.add(ctx, rest)
		if err != nil {
			fatal(err)
		}
		fmt.Println(h)
	case "commit":
		if len(rest) < 2 || rest[0] != "-m" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		h, err := a.commit(ctx, rest[1])
		if err != nil {
			fatal(err)
		}
		fmt.Println(h)
	case "log":
		if err := a.log(ctx, os.Stdout); err != nil {
			fatal(err)
		}
	case "cat":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		h, err := cas.ParseHash(rest[0])
		if err != nil {
			fatal(err)
		}
		if err := a.cat(ctx, h, os.Stdout); err != nil {
			fatal(err)
		}
	case "graph":
		h, err := a.headCommit()
		if err != nil {
			fatal(fmt.Errorf("no commits yet: %w", err))
		}
		res := gitlike.NewResolver(a.repo)
		if err := gitlike.WalkGraph(ctx, res, h, func(ro *gitlike.ResolvedObject) error {
			fmt.Printf("%-12s %s\n", ro.Type, gitlike.PrintObject(ro))
			return nil
		}); err != nil {
			fatal(err)
		}
	case "verify":
		if err := a.verify(ctx); err != nil {
			fatal(err)
		}
	case "stats":
		st, err := a.raw.Stats(ctx)
		if err != nil {
			fatal(err)
		}
		fmt.Println(st)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", cmd, usage)
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
