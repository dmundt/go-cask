package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/verify/adler32"
	"github.com/dmundt/go-cask/cas/verify/crc32"
	"github.com/dmundt/go-cask/cas/verify/crc64"
	"github.com/dmundt/go-cask/cas/verify/sidecar"
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

// unspecifiedCodec is how every surface renders a frame that carries no codec
// identity: a version 1 envelope, a version 2 envelope whose codec declared no
// tag, or bytes that carry no walkable header at all (a raw object). It is the
// documented value rather than a blank cell, so "no codec" is never read as
// "the CLI forgot to report it" (cli.md §2, viewer-design §3).
const unspecifiedCodec = "unspecified"

// codecLabel renders a frame's codec tag for a surface.
func codecLabel(codec string) string {
	if codec == "" {
		return unspecifiedCodec
	}
	return codec
}

// normalizeTypeFilter accepts the documented `<type[@major]>` form: a bare name
// means its first major version, the same reading a stored legacy name gets.
func normalizeTypeFilter(filter string) string {
	if filter == "" || strings.Contains(filter, "@") {
		return filter
	}
	return filter + "@1"
}

// listArgs holds list's flag values.
type listArgs struct {
	limit       int
	offset      int
	jsonOut     bool
	typeFilter  string
	codecFilter string
}

// listFlags registers list's flags over a; opList and the command table both use
// it, so the accepted and the documented flags are one set (cli.md §2, §4).
// -type and -codec filter on the envelope header (cli.md §2): the type is the
// versioned name a bare value is read as ("blob" is "blob@1"), and the codec is
// the identity tag, with "unspecified" naming the frames that carry none.
func listFlags(a *listArgs) *flag.FlagSet {
	flags := newFlagSet("list")
	flags.IntVar(&a.limit, "limit", 100, "max items (1-1000)")
	flags.IntVar(&a.offset, "offset", 0, "start offset")
	flags.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON")
	flags.StringVar(&a.typeFilter, "type", "", "only objects of this envelope type (e.g. blob or blob@1)")
	flags.StringVar(&a.codecFilter, "codec", "", "only objects whose codec tag is this (use \"unspecified\" for frames that carry none)")
	return flags
}

// listItem is one reported object: its address, the algorithm the CLI speaks,
// its stored size, and the three header fields a census reader wants.
type listItem struct {
	// Hash is the object's printable digest.
	Hash string `json:"hash"`
	// Algorithm identifies the digest algorithm.
	Algorithm string `json:"algorithm"`
	// Size is the stored object's byte count.
	Size int64 `json:"size"`
	// Type is the envelope type ("" for bytes that carry no header).
	Type string `json:"type"`
	// Version is the envelope frame version (0 for bytes that carry no
	// walkable header).
	Version byte `json:"version"`
	// Codec is the codec identity tag, or "unspecified" when the frame carries
	// none.
	Codec string `json:"codec"`
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
	digests, err := t.List(ctx)
	if err != nil {
		return err
	}
	total := len(digests)
	skipped := 0
	var items []listItem
	if a.typeFilter != "" || a.codecFilter != "" {
		// A filter needs every object's header, so the walk is the whole store:
		// the alternative would be a walk per page and a total that depends on
		// where the page starts (cli.md §2).
		matched, snapshotSkipped, err := filteredItems(ctx, t, a)
		if err != nil {
			return err
		}
		skipped = snapshotSkipped
		total = len(matched)
		items = index.Paginate(matched, a.offset, a.limit)
	} else {
		// Size the result from the page that is actually reported, not from the
		// whole store: the page is bounded by -limit (cli.md §2).
		page := index.Paginate(digests, a.offset, a.limit)
		items = make([]listItem, 0, len(page))
		for _, h := range page {
			item, ok, err := listItemFor(ctx, t, h)
			if err != nil {
				return err
			}
			if !ok {
				// List reports every digest-named file, including one at a path
				// the layout cannot address (a stray file in the store
				// directory): such an entry is not an object, so it is skipped
				// with a warning instead of failing the whole listing
				// (cas-core §4.4).
				skipped++
				continue
			}
			items = append(items, item)
		}
		if items == nil {
			items = []listItem{}
		}
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

// listItemFor reads one object's reported metadata. It reports ok=false for a
// digest-named file that is not an addressable object, which the caller skips
// with a warning.
func listItemFor(ctx context.Context, t *store.Store, h cas.Digest) (listItem, bool, error) {
	size, err := t.Size(ctx, h)
	if err != nil {
		if errors.Is(err, cas.ErrNotFound) || errors.Is(err, cas.ErrInvalidDigest) {
			return listItem{}, false, nil
		}
		return listItem{}, false, err
	}
	version, codec, typ, err := index.Header(ctx, t, h)
	if err != nil {
		if errors.Is(err, cas.ErrNotFound) || errors.Is(err, cas.ErrInvalidDigest) {
			return listItem{}, false, nil
		}
		return listItem{}, false, err
	}
	return listItem{
		Hash:      sha256.Format(h),
		Algorithm: sha256.Name,
		Size:      size,
		Type:      typ,
		Version:   version,
		Codec:     codecLabel(codec),
	}, true, nil
}

// filteredItems walks the store once and returns the objects a -type/-codec
// filter matches, in the store's listing order. It uses the shared metadata
// snapshot, so the CLI and the viewer agree on what an object's header says.
func filteredItems(ctx context.Context, t *store.Store, a listArgs) ([]listItem, int, error) {
	snapshot, err := index.BuildSnapshot(ctx, t)
	if err != nil {
		return nil, 0, err
	}
	want := normalizeTypeFilter(a.typeFilter)
	items := make([]listItem, 0, len(snapshot.Entries))
	skipped := 0
	for _, entry := range snapshot.Entries {
		if entry.Unreadable {
			// The same skip the unfiltered path applies: a digest-named file
			// that is not a readable object is not a listing result.
			skipped++
			continue
		}
		if want != "" && entry.Type != want {
			continue
		}
		if a.codecFilter != "" && codecLabel(entry.Codec) != a.codecFilter {
			continue
		}
		items = append(items, listItem{
			Hash:      sha256.Format(entry.Digest),
			Algorithm: sha256.Name,
			Size:      entry.Size,
			Type:      entry.Type,
			Version:   entry.Version,
			Codec:     codecLabel(entry.Codec),
		})
	}
	return items, skipped, nil
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
	// The header is read through the shared reader, so the CLI reports the same
	// three fields the list and stats censuses do: a raw object stored by `put`
	// has no envelope (type "", version 0) and a frame without a codec tag
	// reports the explicit "unspecified" rather than a blank.
	version, codec, typ, err := index.Header(ctx, t, h)
	if err != nil {
		return err
	}
	size, err := t.Size(ctx, h)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hash": sha256.Format(h), "algorithm": sha256.Name, "size": size,
			"type": typ, "version": version, "codec": codecLabel(codec),
		})
	}
	fmt.Printf("%s %s size=%d type=%q version=%d codec=%s\n",
		sha256.Format(h), sha256.Name, size, typ, version, codecLabel(codec))
	return nil
}

// --- stats ---

// statsArgs holds stats' flag values.
type statsArgs struct {
	jsonOut bool
}

// statsFlags registers stats' flags over a; opStats and the command table both
// use it, so the accepted and the documented flags are one set (cli.md §2, §4).
func statsFlags(a *statsArgs) *flag.FlagSet {
	flags := newFlagSet("stats")
	flags.BoolVar(&a.jsonOut, "json", false, "machine-readable JSON")
	return flags
}

func opStats(ctx context.Context, t *store.Store, args []string) error {
	var a statsArgs
	flags := statsFlags(&a)
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
	// The census walks the store once and counts the three header fields. An
	// object whose header cannot be read is counted in no axis, so each axis
	// sums to the readable object count and `unreadable` names the rest
	// (cli.md §2).
	snapshot, err := index.BuildSnapshot(ctx, t)
	if err != nil {
		return err
	}
	census := newHeaderCensus(snapshot)
	if a.jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"objects":    st.ObjectCount,
			"bytes":      st.TotalSize,
			"unreadable": census.unreadable,
			"headerless": census.headerless,
			"types":      census.types,
			"versions":   census.versions,
			"codecs":     census.codecs,
		})
	}
	fmt.Println(st)
	census.print(os.Stdout)
	return nil
}

// headerCensus is the per-type, per-version and per-codec tally a `stats`
// report adds. Each map's counts sum to the object count minus the objects that
// carry no header fields to count: `unreadable` (the metadata could not be read)
// and `headerless` (the bytes are not an envelope — a raw object written by
// `put`). A store of enveloped objects therefore sums to its object count
// exactly, and nothing is ever guessed into an axis (cli.md §2).
type headerCensus struct {
	// types counts objects per versioned envelope type.
	types map[string]int
	// versions counts objects per frame version, keyed by its decimal form so
	// the JSON object is stable and ordered by the encoder.
	versions map[string]int
	// codecs counts objects per codec identity tag; "unspecified" is the tag
	// of a frame that carries none.
	codecs map[string]int
	// unreadable counts objects whose metadata could not be read.
	unreadable int
	// headerless counts objects whose bytes carry no walkable envelope header.
	headerless int
}

// newHeaderCensus tallies a built snapshot.
func newHeaderCensus(snapshot *index.Snapshot) headerCensus {
	c := headerCensus{
		types:    make(map[string]int),
		versions: make(map[string]int),
		codecs:   make(map[string]int),
	}
	for _, entry := range snapshot.Entries {
		switch {
		case entry.Unreadable:
			c.unreadable++
		case entry.Type == "":
			// Readable bytes that are not an envelope: nothing to count on any
			// axis, and guessing a version or a type would misdescribe them.
			c.headerless++
		default:
			c.types[entry.Type]++
			c.versions[strconv.Itoa(int(entry.Version))]++
			c.codecs[codecLabel(entry.Codec)]++
		}
	}
	return c
}

// print renders the census as one line per axis, skipping an axis with no
// entries so an empty store prints nothing but its summary.
func (c headerCensus) print(w io.Writer) {
	for _, axis := range []struct {
		label string
		count map[string]int
	}{
		{"types", c.types},
		{"versions", c.versions},
		{"codecs", c.codecs},
	} {
		if len(axis.count) == 0 {
			continue
		}
		keys := make([]string, 0, len(axis.count))
		for key := range axis.count {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", key, axis.count[key]))
		}
		fmt.Fprintf(w, "%s: %s\n", axis.label, strings.Join(parts, ", "))
	}
	if c.headerless > 0 {
		fmt.Fprintf(w, "headerless: %d\n", c.headerless)
	}
	if c.unreadable > 0 {
		fmt.Fprintf(w, "unreadable: %d\n", c.unreadable)
	}
}

// --- verify ---

// verifyArgs holds verify's flag values.
type verifyArgs struct {
	all           bool
	checksums     bool
	checksum      string
	hashAlgorithm string
}

// verifyFlags registers verify's flags over a; opVerify and the command table
// both use it, so the accepted and the documented flags are one set (cli.md
// §2, §4). --all is the cli.md §2 alternative to a single <hash>.
// --checksums switches the check from the object's address to the per-object
// checksum recorded beside it (operations §6), and --checksum names which
// recorded checksum to read. --hash-algo selects the algorithm the address is
// expressed in, so a store addressed by anything but the default is still
// verifiable (cli.md §2).
func verifyFlags(a *verifyArgs) *flag.FlagSet {
	flags := newFlagSet("verify")
	flags.BoolVar(&a.all, "all", false, "verify every object in the store")
	flags.BoolVar(&a.checksums, "checksums", false, "check the per-object checksum recorded beside each object instead of its address")
	flags.StringVar(&a.checksum, "checksum", crc32.Name, "recorded checksum to check with --checksums ("+checksumNames()+")")
	flags.StringVar(&a.hashAlgorithm, "hash-algo", sha256.Name, hashAlgoUsage)
	return flags
}

func opVerify(ctx context.Context, t *store.Store, args []string) error {
	var a verifyArgs
	flags := verifyFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	algorithm, err := lookupDigestAlgorithm(a.hashAlgorithm)
	if err != nil {
		return usagef("invalid hash algorithm %q: %v", a.hashAlgorithm, err)
	}
	checksumSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "checksum" {
			checksumSet = true
		}
	})
	if checksumSet && !a.checksums {
		return usagef("verify --checksum needs --checksums")
	}
	if a.checksums {
		return verifyChecksums(ctx, t, &a, flags, algorithm)
	}
	if a.all {
		if flags.NArg() != 0 {
			return usagef("verify --all takes no additional arguments")
		}
		return verifyAll(ctx, t, algorithm)
	}
	if flags.NArg() != 1 {
		return usagef("verify needs exactly one <hash> or --all")
	}
	h, err := algorithm.Parse(flags.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	if err := t.Verify(ctx, h, algorithm.hasher); err != nil {
		return err
	}
	fmt.Printf("%s ok\n", algorithm.Format(h))
	return nil
}

// checksumNames lists the checksums --checksum accepts, for the usage text.
func checksumNames() string {
	return strings.Join([]string{crc32.Name, adler32.Name, crc64.Name}, ", ")
}

// checksumHasher resolves a shipped checksum by name for the CLI. There is no
// registry in the library — the client owns the algorithm — so the name is
// resolved here, at the one place that has to turn a flag value into a hasher.
func checksumHasher(name string) (cas.Hasher, error) {
	switch name {
	case crc32.Name:
		return crc32.New(), nil
	case adler32.Name:
		return adler32.New(), nil
	case crc64.Name:
		return crc64.New(), nil
	default:
		return nil, usagef("unknown checksum %q (want %s)", name, checksumNames())
	}
}

// verifyChecksums answers the cheap question the recorded checksums exist for:
// do the stored bytes still match the checksum written beside them? It is a
// read-only maintenance check — it never records a checksum and never repairs
// anything (operations §6). The object's address is not re-checked here; that is
// plain `cask verify`.
func verifyChecksums(ctx context.Context, t *store.Store, a *verifyArgs, flags *flag.FlagSet, algorithm digestAlgorithm) error {
	hasher, err := checksumHasher(a.checksum)
	if err != nil {
		return err
	}
	rec, err := sidecar.New(t.Backend)
	if err != nil {
		return err
	}
	verifier := rec.Verifier(a.checksum, hasher)
	if a.all {
		if flags.NArg() != 0 {
			return usagef("verify --all takes no additional arguments")
		}
		return verifyAllChecksums(ctx, verifier, a.checksum)
	}
	if flags.NArg() != 1 {
		return usagef("verify needs exactly one <hash> or --all")
	}
	h, err := algorithm.Parse(flags.Arg(0))
	if err != nil {
		return usagef("invalid hash: %v", err)
	}
	err = verifier.Verify(ctx, h)
	switch {
	case err == nil:
		fmt.Printf("%s %s checksum ok\n", algorithm.Format(h), a.checksum)
		return nil
	case errors.Is(err, sidecar.ErrUnrecorded):
		// Absence is unchecked, not corrupt: nothing was verified and nothing
		// is wrong with the object (#196).
		fmt.Printf("%s no %s checksum record\n", algorithm.Format(h), a.checksum)
		return nil
	case errors.Is(err, cas.ErrCorrupt):
		fmt.Fprintf(os.Stderr, "%s %s: %v\n", checksumMismatchLabel, h, err)
		return fmt.Errorf("%s failed its recorded %s checksum", h, a.checksum)
	default:
		return err
	}
}

// checksumMismatchLabel starts a recorded-checksum mismatch line. It is
// deliberately not "CORRUPT", which cli.md reserves for an object whose bytes no
// longer match its address (cas.ErrDigestMismatch): an operator must be able to
// tell "the store's identity no longer holds" from "the cheap check disagrees",
// and the two have different remedies.
const checksumMismatchLabel = "CHECKSUM MISMATCH"

// verifyAllChecksums checks every record the store's objects have. A mismatch is
// reported per object; an object without a record is counted, not listed —
// listing them is what `verify --checksums <hash>` is for, and a store that
// turned recording on recently has many.
func verifyAllChecksums(ctx context.Context, verifier *sidecar.Verifier, algo string) error {
	report, err := verifier.VerifyAll(ctx)
	if report != nil {
		for _, h := range report.Bad {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", checksumMismatchLabel, h,
				fmt.Errorf("%w: %s failed its recorded %s checksum", cas.ErrCorrupt, h, algo))
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("checked %d recorded objects, %d corrupt, %d unrecorded\n",
		report.Checked, len(report.Bad), len(report.Unrecorded))
	if len(report.Bad) > 0 {
		return fmt.Errorf("%d objects failed their recorded checksum", len(report.Bad))
	}
	return nil
}

// reconcileChecksums drops the record of every object a sweep deleted, so a
// store with sidecars does not accumulate orphan records (operations §6). It is
// a no-op for a store that has no records, and it removes nothing but records:
// the sweep's deleted objects are its only input.
func reconcileChecksums(ctx context.Context, t *store.Store, verb string) error {
	// A backend that cannot name its base keeps its records nowhere, so there is
	// nothing to reconcile and no error to report.
	if _, ok := t.Backend.(interface{ BasePath() string }); !ok {
		return nil
	}
	rec, err := sidecar.New(t.Backend)
	if err != nil {
		return err
	}
	report, err := rec.Reconcile(ctx)
	if err != nil {
		return err
	}
	if report.Records == 0 {
		return nil
	}
	fmt.Printf("%s: checksum records: %d examined, %d orphaned removed, %d objects unrecorded\n",
		verb, report.Records, len(report.Removed), len(report.Unrecorded))
	return nil
}

// verifyAll checks every object the store lists and reports the corrupt ones.
// The sweep runs through cas.VerifyAll, so a backend with no backend-native
// Verify is verified identically (cli.md §2): a digest whose bytes no longer
// match its address is reported (ErrDigestMismatch), while a read failure
// aborts the run instead of being counted as corruption. The algorithm is the
// caller's (`-hash-algo`), because a digest the injected hasher cannot address
// is refused by its width before its bytes are read.
func verifyAll(ctx context.Context, t *store.Store, algorithm digestAlgorithm) error {
	report, err := t.VerifyAll(ctx, algorithm.hasher)
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
	// The sweep just deleted objects, so their records are orphans. Reconcile
	// in the same run instead of leaving them for the next verify to explain.
	return reconcileChecksums(ctx, t, "gc")
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
	// A dry run deletes nothing, so it leaves no orphan record behind and
	// reconciles nothing: the sweep above is the only thing that can create one.
	return reconcileChecksums(ctx, t, "prune")
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
