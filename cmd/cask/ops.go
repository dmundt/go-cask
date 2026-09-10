package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/index"
)

// target is the store the ops speak to: fs.Backend directly (in-process;
// there is no storage service layer — the library is the single source of
// behavior, backend-architecture §2).
type target struct {
	raw *fs.Backend
}

func openTarget(ctx context.Context, mf modeFlags) (*target, error) {
	if mf.store == "" {
		return nil, fmt.Errorf("-store <path> is required")
	}
	raw, err := fs.New(mf.store)
	if err != nil {
		return nil, err
	}
	return &target{raw: raw}, nil
}

// usageError marks an argument error (exit code 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{msg: fmt.Sprintf(format, args...)} }

// pruneCount runs raw.Prune (delete unreachable-from-roots objects older
// than minAge; dryRun reports without deleting) and returns how many objects
// it deleted / would delete.
func pruneCount(ctx context.Context, raw *fs.Backend, roots []cas.Digest, minAge time.Duration, dryRun bool) (int, error) {
	doomed, err := raw.Prune(ctx, roots, minAge, dryRun)
	if err != nil {
		return 0, err
	}
	return len(doomed), nil
}

// --- put ---

func opPut(ctx context.Context, t *target, args []string) error {
	// Flags may follow the positional (spec order: put <file> [-json]), so
	// std flag parsing is not used here.
	jsonOut := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-json":
			jsonOut = true
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		return usagef("put needs exactly one <file|- >")
	}
	var r io.Reader
	if files[0] == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(files[0])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	h, dedup, err := localPut(ctx, t.raw, r)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"hash": sha256.Format(h), "deduplicated": dedup})
	}
	if dedup {
		fmt.Printf("%s (deduplicated)\n", sha256.Format(h))
	} else {
		fmt.Println(sha256.Format(h))
	}
	return nil
}

// localPut stores bytes under the digest of their content, streaming through a
// temp spool while hashing (hash-on-write).
func localPut(ctx context.Context, raw *fs.Backend, r io.Reader) (cas.Digest, bool, error) {
	hasher := sha256.NewHasher()
	spool, err := os.CreateTemp("", "cask-put-*")
	if err != nil {
		return nil, false, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	if _, err := io.Copy(io.MultiWriter(spool, hasher), r); err != nil {
		return nil, false, err
	}
	h := cas.NewDigest(hasher.Sum(nil))
	exists, err := raw.Exists(ctx, h)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			return nil, false, err
		}
		if err := raw.Put(ctx, h, spool); err != nil {
			return nil, false, err
		}
	}
	return h, exists, nil
}

// --- get (default output: stdout) ---

func opGet(ctx context.Context, t *target, args []string) error {
	// Flags may follow the positional (spec order: get <hash> [-o <file>]).
	out := ""
	var hashes []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 >= len(args) {
				return usagef("-o needs a path")
			}
			out, i = args[i+1], i+1
		default:
			hashes = append(hashes, args[i])
		}
	}
	if len(hashes) != 1 {
		return usagef("get needs exactly one <hash>")
	}
	h, err := sha256.Parse(hashes[0])
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	rc, err := t.raw.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()

	w := io.Writer(os.Stdout)
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	_, err = io.Copy(w, rc)
	return err
}

// --- list ---

func opList(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	limit := fs.Int("limit", 100, "max items (1-1000)")
	offset := fs.Int("offset", 0, "start offset")
	jsonOut := fs.Bool("json", false, "machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *limit < 1 || *limit > 1000 {
		return usagef("limit must be between 1 and 1000, got %d", *limit)
	}
	if *offset < 0 {
		return usagef("offset must be >= 0, got %d", *offset)
	}
	type item struct {
		Hash      string `json:"hash"`
		Algorithm string `json:"algorithm"`
		Size      int64  `json:"size"`
	}
	digests, err := t.raw.List(ctx)
	if err != nil {
		return err
	}
	total := len(digests)
	items := make([]item, 0, total)
	for _, h := range index.Paginate(digests, *offset, *limit) {
		size, err := t.raw.Size(ctx, h)
		if err != nil {
			return err
		}
		items = append(items, item{sha256.Format(h), sha256.Name, size})
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"total": total, "objects": items})
	}
	for _, it := range items {
		fmt.Println(it.Hash)
	}
	return nil
}

// --- meta ---

func opMeta(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("meta", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != 1 {
		return usagef("meta needs exactly one <hash>")
	}
	h, err := sha256.Parse(fs.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	rc, err := t.raw.Get(ctx, h)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(rc, 4<<10)) // TLV header carries the type
	rc.Close()
	if err != nil {
		return err
	}
	size, err := t.raw.Size(ctx, h)
	if err != nil {
		return err
	}
	typ := index.EnvelopeType(data)
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hash": sha256.Format(h), "algorithm": sha256.Name, "size": size, "type": typ,
		})
	}
	fmt.Printf("%s %s size=%d type=%q\n", sha256.Format(h), sha256.Name, size, typ)
	return nil
}

// --- stats ---

func opStats(ctx context.Context, t *target, args []string) error {
	if len(args) != 0 {
		return usagef("stats takes no arguments")
	}
	st, err := t.raw.Stats(ctx)
	if err != nil {
		return err
	}
	fmt.Println(st)
	return nil
}

// --- verify ---

func opVerify(ctx context.Context, t *target, args []string) error {
	if len(args) == 0 {
		return usagef("verify needs <hash> or --all")
	}
	if args[0] == "--all" {
		digests, err := t.raw.List(ctx)
		if err != nil {
			return err
		}
		bad := 0
		for _, h := range digests {
			if err := t.raw.Verify(ctx, h, sha256.New()); err != nil {
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
	h, err := sha256.Parse(args[0])
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	if err := t.raw.Verify(ctx, h, sha256.New()); err != nil {
		return err
	}
	fmt.Printf("%s ok\n", sha256.Format(h))
	return nil
}

// --- gc ---

// gcDefaultGrace is the default `gc --min-age`: sweeps reclaim only objects
// older than this, so a concurrent writer's fresh objects are never deleted
// (cas-core §6; Git's gc grace). Pass --min-age 0 for an immediate sweep —
// the dangerous variant, only safe when no other process is writing.
const gcDefaultGrace = 1 * time.Hour

func opGC(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	minAge := fs.Duration("min-age", gcDefaultGrace, "only delete unreachable objects older than this (0 = immediate, dangerous)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	roots, err := parseDigests(fs.Args())
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return usagef("gc needs at least one root hash")
	}
	if *minAge == 0 {
		fmt.Fprintln(os.Stderr, "warning: gc --min-age 0 deletes every unreachable object immediately; only safe when no other process is writing (cas-core §6)")
	}
	// Reachability is the given roots themselves at the byte layer (the
	// store cannot interpret references; graph-aware reachability is the
	// app's job, cas-core §4.11). Only objects older than minAge are
	// reclaimed, so a concurrent writer's recent objects survive the sweep.
	deleted, err := pruneCount(ctx, t.raw, roots, *minAge, false)
	if err != nil {
		return err
	}
	fmt.Printf("gc: deleted %d objects\n", deleted)
	return nil
}

// --- clean ---

func opClean(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	minAge := fs.Duration("min-age", 24*time.Hour, "minimum age of orphan *.tmp files to remove")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != 0 {
		return usagef("clean takes no positional arguments")
	}
	removed, err := t.raw.Clean(ctx, *minAge)
	if err != nil {
		return err
	}
	fmt.Printf("clean: removed %d orphan tmp files\n", removed)
	return nil
}

// --- prune ---

func opPrune(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	minAge := fs.Duration("min-age", gcDefaultGrace, "only delete unreachable objects older than this (0 = immediate, dangerous)")
	dryRun := fs.Bool("dry-run", true, "report without deleting (default true)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	roots, err := parseDigests(fs.Args())
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return usagef("prune needs at least one root hash")
	}
	if *minAge == 0 {
		fmt.Fprintln(os.Stderr, "warning: prune --min-age 0 deletes every unreachable object immediately; only safe when no other process is writing (cas-core §6)")
	}
	doomed, err := t.raw.Prune(ctx, roots, *minAge, *dryRun)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("prune (dry-run): would delete %d objects\n", len(doomed))
		for _, h := range doomed {
			fmt.Printf("  %s\n", h)
		}
		return nil
	}
	fmt.Printf("prune: deleted %d objects\n", len(doomed))
	return nil
}

func parseDigests(args []string) ([]cas.Digest, error) {
	digests := make([]cas.Digest, 0, len(args))
	for _, s := range args {
		h, err := sha256.Parse(s)
		if err != nil {
			return nil, usagef("invalid hash %q: %v", s, err)
		}
		digests = append(digests, h)
	}
	return digests, nil
}
