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
	"github.com/dmundt/go-cask/internal/storage"
)

// target is the store the ops speak to: the library in-process over
// internal/storage. There is no remote target — the product ships no
// network JSON API (backend-architecture §1).
type target struct {
	local *storage.Store
}

func openTarget(ctx context.Context, mf modeFlags) (*target, error) {
	if mf.store == "" {
		return nil, fmt.Errorf("-store <path> is required")
	}
	st, err := storage.New(ctx, storage.Config{Dir: mf.store})
	if err != nil {
		return nil, err
	}
	return &target{local: st}, nil
}

// usageError marks an argument error (exit code 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{msg: fmt.Sprintf(format, args...)} }

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
	h, dedup, err := localPut(ctx, t.local, r, algo)
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
func localPut(ctx context.Context, st *storage.Store, r io.Reader, algo string) (cas.Hash, bool, error) {
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
	exists, err := st.Exists(ctx, h)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			return nil, false, err
		}
		if _, err := st.Put(ctx, h, spool); err != nil {
			return nil, false, err
		}
	}
	return h, exists, nil
}

// --- get / cat ---

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
	rc, err := t.local.Get(ctx, h)
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

func opCat(ctx context.Context, t *target, args []string) error {
	return opGet(ctx, t, append([]string{"-o", ""}, args...))
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
	hashes, err := t.local.List(ctx, *algo)
	if err != nil {
		return err
	}
	total := len(hashes)
	items := make([]item, 0, total)
	for _, h := range index.Paginate(hashes, *offset, *limit) {
		items = append(items, item{h.String(), h.Algorithm(), t.local.Size(h)})
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
	rc, err := t.local.Get(ctx, h)
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
	st, err := t.local.Stats(ctx)
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
		hashes, err := t.local.List(ctx, "")
		if err != nil {
			return err
		}
		bad := 0
		for _, h := range hashes {
			if err := t.local.Verify(ctx, h); err != nil {
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
	if err := t.local.Verify(ctx, h); err != nil {
		return err
	}
	fmt.Printf("%s ok\n", h)
	return nil
}

// --- gc ---

func opGC(ctx context.Context, t *target, args []string) error {
	if len(args) == 0 {
		return usagef("gc needs at least one root hash")
	}
	roots, err := parseHashes(args)
	if err != nil {
		return err
	}
	// Reachability is the given roots themselves at the byte layer (the
	// store cannot interpret references; graph-aware reachability is the
	// app's job, cas-core §4.11).
	reachable := make(map[string]bool, len(roots))
	for _, h := range roots {
		reachable[h.String()] = true
	}
	deleted, err := t.local.GC(ctx, reachable)
	if err != nil {
		return err
	}
	fmt.Printf("gc: deleted %d objects\n", deleted)
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
	doomed, err := t.local.Prune(ctx, roots, *minAge, *dryRun)
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
