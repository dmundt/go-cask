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
	"github.com/dmundt/go-cask/internal/index"
)

// target is the store the ops speak to: cas.FSRawStore directly (in-process;
// there is no storage service layer — the library is the single source of
// behavior, backend-architecture §2).
type target struct {
	raw *cas.FSRawStore
}

func openTarget(ctx context.Context, mf modeFlags) (*target, error) {
	if mf.store == "" {
		return nil, fmt.Errorf("-store <path> is required")
	}
	raw, err := cas.NewFSRawStore(mf.store)
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
func pruneCount(ctx context.Context, raw *cas.FSRawStore, roots []cas.Hash, minAge time.Duration, dryRun bool) (int, error) {
	doomed, err := raw.Prune(ctx, roots, minAge, dryRun)
	if err != nil {
		return 0, err
	}
	return len(doomed), nil
}

// --- put ---

func opPut(ctx context.Context, t *target, args []string) error {
	// Flags may follow the positional (spec order: put <file> [-algo]), so
	// std flag parsing is not used here.
	algo := "sha256"
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-algo":
			if i+1 >= len(args) {
				return usagef("-algo needs a name")
			}
			algo, i = args[i+1], i+1
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
	h, dedup, err := localPut(ctx, t.raw, r, algo)
	if err != nil {
		return err
	}
	if dedup {
		fmt.Printf("%s (deduplicated)\n", h)
	} else {
		fmt.Println(h)
	}
	return nil
}

// localPut stores bytes under the hash of their content with algo,
// streaming through a temp spool (hash-on-write).
func localPut(ctx context.Context, raw *cas.FSRawStore, r io.Reader, algo string) (cas.Hash, bool, error) {
	hasher, err := cas.NewHasher(algo)
	if err != nil {
		return nil, false, err
	}
	spool, err := os.CreateTemp("", "cask-put-*")
	if err != nil {
		return nil, false, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	if _, err := io.Copy(io.MultiWriter(spool, hasher), r); err != nil {
		return nil, false, err
	}
	h, err := cas.NewHash(algo, hasher.Sum(nil))
	if err != nil {
		return nil, false, err
	}
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
	h, err := cas.ParseHash(hashes[0])
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
	algo := fs.String("algo", "", "filter by algorithm")
	limit := fs.Int("limit", 100, "max items (1-1000)")
	offset := fs.Int("offset", 0, "start offset")
	jsonOut := fs.Bool("json", false, "machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	type item struct {
		Hash      string `json:"hash"`
		Algorithm string `json:"algorithm"`
		Size      int64  `json:"size"`
	}
	hashes, err := t.raw.List(ctx, *algo)
	if err != nil {
		return err
	}
	total := len(hashes)
	items := make([]item, 0, total)
	for _, h := range index.Paginate(hashes, *offset, *limit) {
		size, err := t.raw.Size(h)
		if err != nil {
			return err
		}
		items = append(items, item{h.String(), h.Algorithm(), size})
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
	h, err := cas.ParseHash(fs.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	rc, err := t.raw.Get(ctx, h)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	rc.Close()
	if err != nil {
		return err
	}
	size := int64(len(data))
	typ := index.EnvelopeType(data)
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hash": h.String(), "algorithm": h.Algorithm(), "size": size, "type": typ,
		})
	}
	fmt.Printf("%s %s size=%d type=%q\n", h, h.Algorithm(), size, typ)
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
		hashes, err := t.raw.List(ctx, "")
		if err != nil {
			return err
		}
		bad := 0
		for _, h := range hashes {
			if err := t.raw.Verify(ctx, h); err != nil {
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
	h, err := cas.ParseHash(args[0])
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	if err := t.raw.Verify(ctx, h); err != nil {
		return err
	}
	fmt.Printf("%s ok\n", h)
	return nil
}

// --- gc ---

// gcDefaultGrace is the default `gc --min-age`: sweeps reclaim only objects
// older than this, so a concurrent writer's fresh objects are never deleted
// (cas-core §6; Git's gc grace). Pass --min-age 0 for an immediate sweep —
// the dangerous variant, only safe when no other process is writing.
const gcDefaultGrace = 24 * time.Hour

func opGC(ctx context.Context, t *target, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	minAge := fs.Duration("min-age", gcDefaultGrace, "only delete unreachable objects older than this (0 = immediate, dangerous)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	roots, err := parseHashes(fs.Args())
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
	minAge := fs.Duration("min-age", time.Hour, "minimum age of unreachable objects to delete")
	dryRun := fs.Bool("dry-run", true, "report without deleting (default true)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	roots, err := parseHashes(fs.Args())
	if err != nil {
		return err
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

func parseHashes(args []string) ([]cas.Hash, error) {
	hashes := make([]cas.Hash, 0, len(args))
	for _, s := range args {
		h, err := cas.ParseHash(s)
		if err != nil {
			return nil, usagef("invalid hash %q: %v", s, err)
		}
		hashes = append(hashes, h)
	}
	return hashes, nil
}
