package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/index"
	"github.com/dmundt/go-cask/internal/store"
)

// openTarget returns the store the ops speak to: the selected backend opened
// in-process (there is no storage service layer — the library is the single
// source of behavior, backend-architecture §2). The -backend flag selects it,
// and the opened store carries the maintenance capabilities of that backend, so
// every subcommand below is backend-agnostic (backend-architecture §5).
//
// A missing -store or an unknown -backend is a usage error (exit 2); a backend
// failure is a runtime error (exit 1) — the caller classifies the returned
// error (cli.md §3).
func openTarget(ctx context.Context, mf modeFlags) (*store.Store, error) {
	if mf.store == "" {
		return nil, usagef("-store <path> is required")
	}
	kind, err := store.ParseKind(mf.backend)
	if err != nil {
		return nil, usagef("%v", err)
	}
	return store.Open(ctx, store.Options{Kind: kind, Path: mf.store})
}

// usageError marks an argument error (exit code 2).
type usageError struct{ msg string }

// Error implements the error interface.
func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{msg: fmt.Sprintf(format, args...)} }

// reachableSet builds the byte-layer reachable set from the root digests the
// user listed. The set is complete as given: the store cannot interpret
// references, so cask cannot expand a root into what it points to —
// graph-aware reachability is the app's job (cas-core §4.11, cas.Reachable).
// Pass every digest that must survive, not just entry points, or a sweep will
// delete what they reference.
func reachableSet(roots []cas.Digest) map[string]bool {
	reachable := make(map[string]bool, len(roots))
	for _, r := range roots {
		reachable[r.String()] = true
	}
	return reachable
}

// --- put ---

// putArgs holds put's flag values.
type putArgs struct {
	jsonOut bool
}

// putFlags registers put's flags over a; opPut and the command table both use
// it, so the accepted and the documented flags are one set (cli.md §2, §4).
func putFlags(a *putArgs) *flag.FlagSet {
	flags := newFlagSet("put")
	flags.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON")
	return flags
}

func opPut(ctx context.Context, t *store.Store, args []string) error {
	var a putArgs
	flags := putFlags(&a)
	// Flags may follow the operand (cli.md §2: "put <file> [-json]"), so the
	// arguments are parsed by position-independent operand parsing.
	if err := parseOperands(flags, args); err != nil {
		return err
	}
	files := flags.Args()
	if len(files) != 1 {
		return usagef("put needs exactly one <file|->")
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
	h, dedup, err := localPut(ctx, t, r)
	if err != nil {
		return err
	}
	if a.jsonOut {
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
// temp spool while hashing (hash-on-write). It takes the minimal Backend
// contract, so the same write path serves every backend.
func localPut(ctx context.Context, backend cas.Backend, r io.Reader) (cas.Digest, bool, error) {
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
	exists, err := backend.Exists(ctx, h)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			return nil, false, err
		}
		if err := backend.Put(ctx, h, spool); err != nil {
			return nil, false, err
		}
	}
	return h, exists, nil
}

// --- get (default output: stdout) ---

// getArgs holds get's flag values.
type getArgs struct {
	out string
}

// getFlags registers get's flags over a; opGet and the command table both use
// it, so the accepted and the documented flags are one set (cli.md §2, §4).
func getFlags(a *getArgs) *flag.FlagSet {
	flags := newFlagSet("get")
	flags.StringVar(&a.out, "o", "", "write to this file instead of stdout")
	return flags
}

func opGet(ctx context.Context, t *store.Store, args []string) error {
	var a getArgs
	flags := getFlags(&a)
	// Flags may follow the operand (cli.md §2: "get <hash> [-o <file>]").
	if err := parseOperands(flags, args); err != nil {
		return err
	}
	hashes := flags.Args()
	if len(hashes) != 1 {
		return usagef("get needs exactly one <hash>")
	}
	h, err := sha256.Parse(hashes[0])
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	rc, err := t.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()

	w := io.Writer(os.Stdout)
	if a.out != "" {
		f, err := os.Create(a.out)
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

// listArgs holds list's flag values.
type listArgs struct {
	limit   int
	offset  int
	jsonOut bool
}

// listFlags registers list's flags over a; opList and the command table both use
// it, so the accepted and the documented flags are one set (cli.md §2, §4).
func listFlags(a *listArgs) *flag.FlagSet {
	flags := newFlagSet("list")
	flags.IntVar(&a.limit, "limit", 100, "max items (1-1000)")
	flags.IntVar(&a.offset, "offset", 0, "start offset")
	flags.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON")
	return flags
}

func opList(ctx context.Context, t *store.Store, args []string) error {
	var a listArgs
	flags := listFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if a.limit < 1 || a.limit > 1000 {
		return usagef("limit must be between 1 and 1000, got %d", a.limit)
	}
	if a.offset < 0 {
		return usagef("offset must be >= 0, got %d", a.offset)
	}
	type item struct {
		// Hash is the object's printable digest.
		Hash string `json:"hash"`
		// Algorithm identifies the digest algorithm.
		Algorithm string `json:"algorithm"`
		// Size is the stored object's byte count.
		Size int64 `json:"size"`
	}
	digests, err := t.List(ctx)
	if err != nil {
		return err
	}
	total := len(digests)
	// Size the result from the page that is actually reported, not from the
	// whole store: the page is bounded by -limit (cli.md §2).
	page := index.Paginate(digests, a.offset, a.limit)
	items := make([]item, 0, len(page))
	skipped := 0
	for _, h := range page {
		size, err := t.Size(ctx, h)
		if err != nil {
			// List reports every digest-named file, including one at a path the
			// layout cannot address (a stray file in the store directory): such
			// an entry is not an object, so it is skipped with a warning instead
			// of failing the whole listing (cas-core §4.4).
			if errors.Is(err, cas.ErrNotFound) || errors.Is(err, cas.ErrInvalidDigest) {
				skipped++
				continue
			}
			return err
		}
		items = append(items, item{sha256.Format(h), sha256.Name, size})
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "cask: skipped %d digest-named file(s) that are not readable objects\n", skipped)
	}
	if a.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"total": total, "objects": items})
	}
	for _, it := range items {
		fmt.Println(it.Hash)
	}
	return nil
}

// --- meta ---

// metaArgs holds meta's flag values.
type metaArgs struct {
	jsonOut bool
}

// metaFlags registers meta's flags over a; opMeta and the command table both
// use it, so the accepted and the documented flags are one set (cli.md §2, §4).
func metaFlags(a *metaArgs) *flag.FlagSet {
	flags := newFlagSet("meta")
	flags.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON")
	return flags
}

func opMeta(ctx context.Context, t *store.Store, args []string) error {
	var a metaArgs
	flags := metaFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return usagef("meta needs exactly one <hash>")
	}
	h, err := sha256.Parse(flags.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	rc, err := t.Get(ctx, h)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(rc, 4<<10)) // TLV header carries the type
	rc.Close()
	if err != nil {
		return err
	}
	size, err := t.Size(ctx, h)
	if err != nil {
		return err
	}
	typ := index.EnvelopeType(data)
	if a.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hash": sha256.Format(h), "algorithm": sha256.Name, "size": size, "type": typ,
		})
	}
	fmt.Printf("%s %s size=%d type=%q\n", sha256.Format(h), sha256.Name, size, typ)
	return nil
}

// --- stats ---

// statsFlags registers stats' flags: the command takes none, but parsing its
// arguments gives it the same -h/-help handling and unknown-flag rejection as
// every other command (cli.md §4).
func statsFlags() *flag.FlagSet {
	return newFlagSet("stats")
}

func opStats(ctx context.Context, t *store.Store, args []string) error {
	flags := statsFlags()
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usagef("stats takes no arguments")
	}
	st, err := t.Stats(ctx)
	if err != nil {
		return err
	}
	fmt.Println(st)
	return nil
}

// --- verify ---

// verifyArgs holds verify's flag values.
type verifyArgs struct {
	all bool
}

// verifyFlags registers verify's flags over a; opVerify and the command table
// both use it, so the accepted and the documented flags are one set (cli.md
// §2, §4). --all is the cli.md §2 alternative to a single <hash>.
func verifyFlags(a *verifyArgs) *flag.FlagSet {
	flags := newFlagSet("verify")
	flags.BoolVar(&a.all, "all", false, "verify every object in the store")
	return flags
}

func opVerify(ctx context.Context, t *store.Store, args []string) error {
	var a verifyArgs
	flags := verifyFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if a.all {
		if flags.NArg() != 0 {
			return usagef("verify --all takes no additional arguments")
		}
		return verifyAll(ctx, t)
	}
	if flags.NArg() != 1 {
		return usagef("verify needs exactly one <hash> or --all")
	}
	h, err := sha256.Parse(flags.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	if err := t.Verify(ctx, h, sha256.New()); err != nil {
		return err
	}
	fmt.Printf("%s ok\n", sha256.Format(h))
	return nil
}

// verifyAll checks every object the store lists and reports the corrupt ones.
// The sweep runs through cas.VerifyAll, so a backend with no backend-native
// Verify is verified identically (cli.md §2): a digest whose bytes no longer
// match its address is reported (ErrDigestMismatch), while a read failure
// aborts the run instead of being counted as corruption.
func verifyAll(ctx context.Context, t *store.Store) error {
	report, err := t.VerifyAll(ctx, sha256.New())
	if report != nil {
		for _, h := range report.Bad {
			// The digest is the whole diagnosis for a mismatch; render it the
			// way cas.Verify does, so the message is unchanged from the
			// per-object check.
			fmt.Fprintf(os.Stderr, "CORRUPT %s: %v\n", h, fmt.Errorf("%w: %s", cas.ErrDigestMismatch, h))
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("verified %d objects, %d corrupt\n", report.Checked, len(report.Bad))
	if len(report.Bad) > 0 {
		return fmt.Errorf("%d corrupt objects", len(report.Bad))
	}
	return nil
}

// --- gc ---

// gcDefaultGrace is the default `gc --min-age`: sweeps reclaim only objects
// older than this, so a concurrent writer's fresh objects are never deleted
// (cas-core §6; Git's gc grace). Pass --min-age 0 for an immediate sweep —
// the dangerous variant, only safe when no other process is writing.
const gcDefaultGrace = 1 * time.Hour

// gcArgs holds gc's flag values.
type gcArgs struct {
	minAge time.Duration
}

// gcFlags registers gc's flags over a; opGC and the command table both use it,
// so the accepted and the documented flags are one set (cli.md §2, §4).
func gcFlags(a *gcArgs) *flag.FlagSet {
	flags := newFlagSet("gc")
	flags.DurationVar(&a.minAge, "min-age", gcDefaultGrace, "only delete unreachable objects older than this (0 = immediate, dangerous)")
	return flags
}

func opGC(ctx context.Context, t *store.Store, args []string) error {
	var a gcArgs
	flags := gcFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if a.minAge < 0 {
		return usagef("min-age must be >= 0, got %s", a.minAge)
	}
	roots, err := parseDigests(flags.Args())
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return usagef("gc needs at least one root hash")
	}
	if a.minAge == 0 {
		fmt.Fprintln(os.Stderr, "warning: gc --min-age 0 deletes every unreachable object immediately; only safe when no other process is writing (cas-core §6)")
	}
	// Only objects older than minAge are reclaimed, so a concurrent writer's
	// recent objects survive the sweep. The store drives the sweep itself —
	// the backend's native Prune when it has one, the portable cas.Sweep
	// otherwise — so gc works over a packed store too (backend-architecture
	// §5). roots is the complete reachable set at the byte layer: the store
	// cannot interpret references; graph-aware reachability is the app's job
	// (cas-core §4.11).
	doomed, err := t.Sweep(ctx, "gc", reachableSet(roots), a.minAge, false)
	if err != nil {
		return err
	}
	fmt.Printf("gc: deleted %d objects\n", len(doomed))
	return nil
}

// --- clean ---

// cleanArgs holds clean's flag values.
type cleanArgs struct {
	minAge time.Duration
}

// cleanFlags registers clean's flags over a; opClean and the command table both
// use it, so the accepted and the documented flags are one set (cli.md §2, §4).
func cleanFlags(a *cleanArgs) *flag.FlagSet {
	flags := newFlagSet("clean")
	flags.DurationVar(&a.minAge, "min-age", 24*time.Hour, "minimum age of orphan *.tmp files to remove")
	return flags
}

func opClean(ctx context.Context, t *store.Store, args []string) error {
	var a cleanArgs
	flags := cleanFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if a.minAge < 0 {
		return usagef("min-age must be >= 0, got %s", a.minAge)
	}
	if flags.NArg() != 0 {
		return usagef("clean takes no positional arguments")
	}
	removed, err := t.Clean(ctx, a.minAge)
	if err != nil {
		return err
	}
	fmt.Printf("clean: removed %d orphan tmp files\n", removed)
	return nil
}

// --- prune ---

// pruneArgs holds prune's flag values.
type pruneArgs struct {
	minAge time.Duration
	dryRun bool
}

// pruneFlags registers prune's flags over a; opPrune and the command table both
// use it, so the accepted and the documented flags are one set (cli.md §2, §4).
func pruneFlags(a *pruneArgs) *flag.FlagSet {
	flags := newFlagSet("prune")
	flags.DurationVar(&a.minAge, "min-age", gcDefaultGrace, "only delete unreachable objects older than this (0 = immediate, dangerous)")
	flags.BoolVar(&a.dryRun, "dry-run", true, "report without deleting (default true)")
	return flags
}

func opPrune(ctx context.Context, t *store.Store, args []string) error {
	var a pruneArgs
	flags := pruneFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if a.minAge < 0 {
		return usagef("min-age must be >= 0, got %s", a.minAge)
	}
	roots, err := parseDigests(flags.Args())
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return usagef("prune needs at least one root hash")
	}
	if a.minAge == 0 {
		fmt.Fprintln(os.Stderr, "warning: prune --min-age 0 deletes every unreachable object immediately; only safe when no other process is writing (cas-core §6)")
	}
	// roots is the complete reachable set at the byte layer (the store
	// cannot interpret references; graph-aware reachability is the app's
	// job, cas-core §4.11) — pass every digest that must survive, not just
	// entry points. The store picks the backend's native Prune when it has
	// one and the portable cas.Sweep otherwise (backend-architecture §5).
	doomed, err := t.Sweep(ctx, "prune", reachableSet(roots), a.minAge, a.dryRun)
	if err != nil {
		return err
	}
	if a.dryRun {
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
