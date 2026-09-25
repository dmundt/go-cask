package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/verify/adler32"
	"github.com/dmundt/go-cask/cas/verify/crc32"
	"github.com/dmundt/go-cask/cas/verify/crc64"
	"github.com/dmundt/go-cask/cas/verify/sidecar"
	"github.com/dmundt/go-cask/internal/store"
)

// erroringBackend is a cas.Backend that lifts the errors an operation must
// propagate out of states a store on disk cannot be arranged into — a backend
// failing mid-sweep, not a store containing bad bytes — through the very
// interface the CLI talks to. Every nil field delegates to the embedded healthy
// backend, so one value injects exactly one fault and otherwise behaves as a
// healthy store.
//
// The healthy backend is held in a named field rather than embedded: an
// embedded interface does NOT promote its methods into the wrapper's method set
// (only an embedded struct does), so the wrapper would stop satisfying
// cas.Statter and store.Store.Size would refuse to read an object's size. Every
// delegated method is therefore spelled out, and the type assertion below is
// the compile-time guard that the list stays complete.
type erroringBackend struct {
	healthy cas.Backend
	listErr error
	// listAfter is the 1-based listing call the fault is armed for; 0 means the
	// first one, so a plain newErroringBackend faults every listing. A caller
	// that lists once before the call under test (opList does) arms the fault
	// for the second, which is the one the code under test performs.
	listAfter int
	listCalls int
	getErr    error
	existsErr error
	putErr    error
	sizeErr   error
	statsErr  error
}

// The CLI's metadata path type-asserts its backend to cas.Statter (store.Store
// .Size/ModTime), so the wrapper must keep that capability of the backend it
// decorates. The binding is compile-time: dropping a delegated method fails the
// build here instead of silently reporting "size is not supported".
var _ cas.Statter = (*erroringBackend)(nil)

// newErroringBackend decorates a healthy backend with the named fault.
func newErroringBackend(healthy cas.Backend, fault string, err error) *erroringBackend {
	return newErroringBackendAt(healthy, fault, err, 1)
}

// newErroringBackendAt decorates a healthy backend with the named fault, armed
// for the callAt-th invocation of that method (1-based).
func newErroringBackendAt(healthy cas.Backend, fault string, err error, callAt int) *erroringBackend {
	b := &erroringBackend{healthy: healthy, listAfter: callAt}
	switch fault {
	case "list":
		b.listErr = err
	case "get":
		b.getErr = err
	case "exists":
		b.existsErr = err
	case "put":
		b.putErr = err
	case "size":
		b.sizeErr = err
	case "stats":
		b.statsErr = err
	default:
		panic("unknown fault " + fault)
	}
	return b
}

// List returns the injected error on the armed call, and the healthy backend's
// listing otherwise.
func (b *erroringBackend) List(ctx context.Context) ([]cas.Digest, error) {
	b.listCalls++
	if b.listErr != nil && (b.listAfter <= 1 || b.listCalls >= b.listAfter) {
		return nil, b.listErr
	}
	return b.healthy.List(ctx)
}

// Get returns the injected error, or the healthy backend's reader.
func (b *erroringBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if b.getErr != nil {
		return nil, b.getErr
	}
	return b.healthy.Get(ctx, d)
}

// Exists returns the injected error, or the healthy backend's answer.
func (b *erroringBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if b.existsErr != nil {
		return false, b.existsErr
	}
	return b.healthy.Exists(ctx, d)
}

// Put returns the injected error, or stores through the healthy backend.
func (b *erroringBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if b.putErr != nil {
		return b.putErr
	}
	return b.healthy.Put(ctx, d, r)
}

// Delete passes through to the healthy backend.
func (b *erroringBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.healthy.Delete(ctx, d)
}

// Stats returns the injected error, or the healthy backend's totals.
func (b *erroringBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	if b.statsErr != nil {
		return nil, b.statsErr
	}
	return b.healthy.Stats(ctx)
}

// Size returns the injected error, or the healthy backend's physical metadata.
func (b *erroringBackend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	if b.sizeErr != nil {
		return 0, b.sizeErr
	}
	statter, ok := b.healthy.(cas.Statter)
	if !ok {
		return 0, cas.ErrUnsupported
	}
	return statter.Size(ctx, d)
}

// ModTime passes through to the healthy backend's physical metadata.
func (b *erroringBackend) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	statter, ok := b.healthy.(cas.Statter)
	if !ok {
		return time.Time{}, cas.ErrUnsupported
	}
	return statter.ModTime(ctx, d)
}

// BasePath passes through to the healthy backend, so a store opened through the
// wrapper still locates the record directory beside its own bytes (the same
// structural interface fs.Backend and packfs.Backend report). It is spelled out
// for the same reason as Size and ModTime: a promoted method of an embedded
// interface does not join the wrapper's method set.
func (b *erroringBackend) BasePath() string {
	reporter, ok := b.healthy.(interface{ BasePath() string })
	if !ok {
		return ""
	}
	return reporter.BasePath()
}

// openedStore is an opened store over a real filesystem backend: the concrete
// store a fault is injected into, plus the filesystem backend that provides the
// metadata methods (Size, ModTime) the CLI's reporting path needs.
type openedStore struct {
	store   *store.Store
	backend *fs.Backend
}

// openStore opens path as a real fs store, returning the store and the concrete
// backend so a test can decorate store.Backend with an erroringBackend that
// still answers Size and ModTime.
func openStore(t *testing.T, path string) openedStore {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Kind: store.KindFS, Path: path})
	if err != nil {
		t.Fatalf("store.Open(%q): %v", path, err)
	}
	backend, ok := st.Backend.(*fs.Backend)
	if !ok {
		t.Fatalf("store.Open(%q) opened a %T, want the fs backend", path, st.Backend)
	}
	t.Cleanup(func() { _ = st.Close() })
	return openedStore{store: st, backend: backend}
}

// faulty returns a store of the same kind and capabilities whose backend
// injects err on fault, so an operation runs against a store whose bytes are
// sound but whose backend fails where the test says. The store is built rather
// than copied: a store.Store carries a sync.Once and is not copyable.
func (o openedStore) faulty(fault string, err error) *store.Store {
	return o.faultyAt(fault, err, 1)
}

// faultyAt is faulty with the fault armed for the callAt-th invocation of the
// faulted method, for an operation that calls it more than once before the call
// under test.
func (o openedStore) faultyAt(fault string, err error, callAt int) *store.Store {
	return &store.Store{
		Backend:      newErroringBackendAt(o.backend, fault, err, callAt),
		Capabilities: o.store.Capabilities,
		Kind:         o.store.Kind,
	}
}

// runQuiet runs one operation with both streams captured and discarded: a test
// that asserts an operation's returned error needs no terminal output, and the
// CLI writes its diagnostics to the process streams.
func runQuiet(t *testing.T, mf modeFlags, cmd string, args ...string) int {
	t.Helper()
	_, _, code := runBoth(t, mf, cmd, args...)
	return code
}

// Uncovered in ops.go, with the reason each branch has no deterministic test
// (testing-strategy §5):
//
//   - localPut's spool rewind (ops.go:142-143) seeks a file localPut created
//     with os.CreateTemp; no input can make that Seek fail, so the branch is
//     unreachable rather than merely untested.
//   - the unfiltered listing's empty-page normalization (ops.go:306-307) is
//     reached in behaviour — TestListSkipsAStrayDigestNamedFile stores a
//     digest-named file at a path the layout cannot address and asserts the
//     `{"total":1,"objects":[]}` result that slice exists for — but the
//     instrumentation attributes the enclosing block elsewhere, so this
//     specific range stays uncounted.
//   - verifyChecksums' sidecar.New error (ops.go:669-670) and
//     reconcileChecksums' sidecar.New error (ops.go:745-746) need a backend that
//     reports a base path AND disagrees with the base the record directory is
//     derived from. The CLI's only backends (fs, packfs) report one base and
//     expose no way to override it, so sidecar.New cannot fail for a store the
//     CLI opened; reconcileChecksums' guard above it only ever calls
//     sidecar.New for a backend that does report one.
//   - opClean's Clean failure (ops.go:872-873) needs a backend that implements
//     cas.Cleaner and then fails. The one backend the CLI opens implements it by
//     delegating to the filesystem's own temp sweep, which reports no error for
//     a store whose directory was just created — the success shape every `clean`
//     test exercises.

// TestFilteredItemsReturnsAnEmptyMatchSet pins the filtered listing's contract
// directly: a filter that matches nothing yields an empty, non-nil item slice
// with no skip count and no error, which is what makes `list -json` print
// `"objects": []` for a value no object carries instead of failing the command
// (cli.md §2).
func TestFilteredItemsReturnsAnEmptyMatchSet(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "match nothing")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)

	items, skipped, err := filteredItems(context.Background(), opened.store, listArgs{typeFilter: "nothing@1"})
	if err != nil {
		t.Fatalf("filteredItems = %v, want no error", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("filteredItems = %v, want an empty, non-nil match set", items)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0: the fixture's one object is readable", skipped)
	}

	// The matching filter returns that object, so the empty result above is the
	// filter's doing and not an empty store.
	matched, skipped, err := filteredItems(context.Background(), opened.store, listArgs{})
	if err != nil {
		t.Fatalf("unfiltered filteredItems = %v, want no error", err)
	}
	if len(matched) != 1 {
		t.Fatalf("unfiltered filteredItems = %d objects, want the one stored", len(matched))
	}
	if skipped != 0 {
		t.Fatalf("unfiltered skipped = %d, want 0", skipped)
	}

	// A -codec filter that matches nothing drops the entry the -type filter
	// alone would have kept, so both filters are applied and neither is
	// short-circuited by the other.
	byCodec, skipped, err := filteredItems(context.Background(), opened.store, listArgs{codecFilter: "no-such-codec"})
	if err != nil {
		t.Fatalf("filteredItems by codec = %v, want no error", err)
	}
	if len(byCodec) != 0 {
		t.Fatalf("filteredItems by codec = %d objects, want none matching", len(byCodec))
	}
	if skipped != 0 {
		t.Fatalf("codec-filtered skipped = %d, want 0", skipped)
	}
}

// TestPutReportsAMissingFile pins the file operand's read failure: a path that
// does not exist is a runtime error (exit 1) naming the file, and nothing is
// stored — the operand count is checked first, so this is the open that fails,
// not the arity (cli.md §2, §3).
func TestPutReportsAMissingFile(t *testing.T) {
	mf := localMF(t)
	missing := filepath.Join(t.TempDir(), "absent.bin")

	_, stderr, code := runBoth(t, mf, "put", missing)
	if code != 1 {
		t.Fatalf("put on a missing file exit = %d, want 1 (runtime)", code)
	}
	if !strings.Contains(stderr, "absent.bin") {
		t.Fatalf("stderr = %q, want it to name the missing file", stderr)
	}
	out, code := run(t, mf, "list")
	if code != 0 {
		t.Fatal("list failed")
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("a failed put stored %q, want the store untouched", out)
	}
}

// TestUsageErrorBranches pins the operand-arity and positional-argument
// rejections cli.md §3 makes usage errors (exit 2): one named case per branch,
// so a regression names the check that was lost.
func TestUsageErrorBranches(t *testing.T) {
	mf := localMF(t)
	digest := "sha256:" + strings.Repeat("00", 32)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"get without a hash", []string{"get"}},
		{"get with two hashes", []string{"get", digest, digest}},
		{"meta without a hash", []string{"meta"}},
		{"meta with two hashes", []string{"meta", digest, digest}},
		{"stats with an operand", []string{"stats", "extra"}},
		{"clean with an operand", []string{"clean", "extra"}},
		{"meta with an unknown flag", []string{"meta", "-nope", digest}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := runQuiet(t, mf, tc.args[0], tc.args[1:]...); code != 2 {
				t.Fatalf("%v exit = %d, want 2 (usage)", tc.args, code)
			}
		})
	}
}

// TestOpRejectsInvalidHash closes the invalid-<hash> branch of get, meta and
// verify: cli.md §3 makes a malformed digest a usage error (2), and the message
// names the value the operator typed instead of failing later as a miss.
func TestOpRejectsInvalidHash(t *testing.T) {
	mf := localMF(t)
	for _, command := range []string{"get", "meta", "verify"} {
		t.Run(command, func(t *testing.T) {
			_, stderr, code := runBoth(t, mf, command, "not-a-digest")
			if code != 2 {
				t.Errorf("%s not-a-digest exit = %d, want 2 (usage)", command, code)
			}
			if !strings.Contains(stderr, "invalid hash") {
				t.Errorf("%s stderr = %q, want it to name the invalid hash", command, stderr)
			}
		})
	}
}

// errReader fails every read with err.
type errReader struct{ err error }

// Read implements io.Reader.
func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestFailingReaderIsReportedWithNothingStored pins the read path's first
// failure: bytes the caller's reader cannot deliver are reported with that
// reader's own error (wrapped, so errors.Is still finds it) and nothing is
// stored.
func TestFailingReaderIsReportedWithNothingStored(t *testing.T) {
	backend := backmem.New()
	want := errors.New("spool read failed")
	_, _, err := localPut(context.Background(), backend, io.MultiReader(
		strings.NewReader("partial"), errReader{err: want}))
	if !errors.Is(err, want) {
		t.Fatalf("localPut with a failing reader = %v, want %v", err, want)
	}
	digests, listErr := backend.List(context.Background())
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(digests) != 0 {
		t.Fatalf("a failed put left %d objects behind, want none", len(digests))
	}
}

// TestLocalPutBackendFaults pins the two backend faults on the write path: a
// failed existence probe and a failed store are both reported, and a failed
// store leaves the store empty.
func TestLocalPutBackendFaults(t *testing.T) {
	ctx := context.Background()
	t.Run("exists probe fails", func(t *testing.T) {
		want := errors.New("probe failed")
		backend := newErroringBackend(backmem.New(), "exists", want)
		if _, _, err := localPut(ctx, backend, strings.NewReader("payload")); !errors.Is(err, want) {
			t.Fatalf("localPut with a failing Exists = %v, want %v", err, want)
		}
	})
	t.Run("store fails", func(t *testing.T) {
		want := errors.New("put failed")
		backend := newErroringBackend(backmem.New(), "put", want)
		if _, _, err := localPut(ctx, backend, strings.NewReader("payload")); !errors.Is(err, want) {
			t.Fatalf("localPut with a failing Put = %v, want %v", err, want)
		}
		digests, err := backend.healthy.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(digests) != 0 {
			t.Fatalf("a failed Put stored %d objects, want none", len(digests))
		}
	})
}

// TestPutReportsAFailingStore pins the standalone write path's store failure:
// `put` through the CLI reports the backend's error (exit 1) and stores nothing,
// which is the localPut branch the seed-preview path does not cover.
func TestPutReportsAFailingStore(t *testing.T) {
	mf := localMF(t)
	opened := openStore(t, mf.store)
	want := errors.New("store rejected the write")
	st := opened.faulty("put", want)

	var opErr error
	stdout, _ := captureStreams(t, func() {
		spec, ok := command("put")
		if !ok {
			t.Fatal("put is not a registered command")
		}
		opErr = spec.op(context.Background(), st, []string{writeTemp(t, "refused bytes")})
	})
	if !errors.Is(opErr, want) {
		t.Fatalf("put over a rejecting store = %v, want %v", opErr, want)
	}
	if stdout != "" {
		t.Fatalf("put printed %q, want no hash for a write that failed", stdout)
	}

	var code int
	_, reported := captureStreams(t, func() { code = reportError(opErr) })
	if code != 1 {
		t.Fatalf("reportError(%v) = %d, want 1 (runtime)", opErr, code)
	}
	if !strings.Contains(reported, want.Error()) {
		t.Fatalf("stderr = %q, want the backend's message", reported)
	}
}

// The rewind localPut performs before storing the spooled bytes is deliberately
// left uncovered: it seeks an os.CreateTemp file, and no deterministic input can
// make that Seek fail — the spool is a real file the function itself created. A
// reader that fails the rewind (a seekErrorReader over an os.File is still an
// os.File) cannot replace it, so the branch is untestable rather than untested
// (testing-strategy §5: an unreachable defensive branch stays uncovered and says
// so).

// TestGetReportsUnwritableOutput pins the -o failure: a destination the process
// cannot create is a runtime error (exit 1), not a usage error, and the object
// itself is unaffected.
func TestGetReportsUnwritableOutput(t *testing.T) {
	mf := localMF(t)
	out, code := run(t, mf, "put", writeTemp(t, "content for -o"))
	if code != 0 {
		t.Fatalf("put exit %d", code)
	}
	hash := strings.TrimSpace(out)

	// A path below a regular file can never be created: the parent is not a
	// directory.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runBoth(t, mf, "get", hash, "-o", filepath.Join(blocker, "out.bin"))
	if code != 1 {
		t.Fatalf("get -o below a file: exit %d, want 1 (runtime)", code)
	}
	// The wording is the filesystem's, and it differs by platform ("not a
	// directory" on POSIX, "The system cannot find the path specified." on
	// Windows), so the assertion is that the failure names the destination the
	// caller asked for.
	if !strings.Contains(stderr, "out.bin") {
		t.Fatalf("get -o stderr = %q, want the filesystem's own error naming the destination", stderr)
	}
	if _, _, code := runBoth(t, mf, "get", hash); code != 0 {
		t.Fatal("the failed -o write damaged the object")
	}
}

// TestListSkipsAStrayDigestNamedFile pins the documented stray-file behavior
// (cli.md §2, cas-core §4.4): a digest-named file at a path the fan-out layout
// cannot address is listed by the backend but is not an object, so it is
// skipped with a stderr warning and the command still succeeds. It is also the
// case that leaves the unfiltered page's item slice empty rather than nil, so
// `list -json` reports `[]` — an empty array, never null.
func TestListSkipsAStrayDigestNamedFile(t *testing.T) {
	mf := localMF(t)
	stray := strings.Repeat("11", 32)
	if err := os.WriteFile(filepath.Join(mf.store, stray), []byte("not at the layout path"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, stderr, code := runBoth(t, mf, "list")
	if code != 0 {
		t.Fatalf("list exit = %d, want 0 (a stray file is skipped, not fatal): %s", code, stderr)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("list printed %q, want no object lines for a stray file", out)
	}
	if !strings.Contains(stderr, "cask: skipped 1 digest-named file(s) that are not readable objects") {
		t.Fatalf("list stderr = %q, want the skipped-file warning", stderr)
	}

	// The page's item slice is empty rather than nil, so the JSON carries an
	// empty array and not null — a machine reader always sees a list.
	out, _, code = runBoth(t, mf, "list", "-json")
	if code != 0 {
		t.Fatalf("list -json exit = %d, want 0", code)
	}
	if !strings.Contains(out, `"objects":[]`) {
		t.Fatalf("list -json = %q, want an empty objects array rather than null", out)
	}
}

// TestListReportsStoreWalkFailure pins the listing's runtime error: a store
// whose listing fails is reported instead of being printed as an empty store.
// The filtered path walks the store twice — opList lists once up front and
// filteredItems lists again through the snapshot — so its fault is armed for the
// second listing, the one that path owns. The census is driven with the fault on
// its first listing, because its backend totals report sizes rather than a
// listing and its snapshot is the only walk it makes (TestStatsTotalsDoNotList
// pins that split).
func TestListReportsStoreWalkFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "listed")); code != 0 {
		t.Fatal("put failed")
	}
	want := errors.New("list failed")

	t.Run("plain list, the first listing", func(t *testing.T) {
		opened := openStore(t, mf.store)
		if err := opList(context.Background(), opened.faulty("list", want), nil); !errors.Is(err, want) {
			t.Fatalf("opList = %v, want %v", err, want)
		}
	})

	t.Run("filtered list, the snapshot listing", func(t *testing.T) {
		opened := openStore(t, mf.store)
		st := opened.faultyAt("list", want, 2)
		if err := opList(context.Background(), st, []string{"-type", "blob@1"}); !errors.Is(err, want) {
			t.Fatalf("filtered opList = %v, want %v", err, want)
		}
	})

	t.Run("filtered items, the snapshot listing", func(t *testing.T) {
		opened := openStore(t, mf.store)
		if _, _, err := filteredItems(context.Background(), opened.faulty("list", want), listArgs{typeFilter: "blob@1"}); !errors.Is(err, want) {
			t.Fatalf("filteredItems = %v, want %v", err, want)
		}
	})

	t.Run("stats census, the snapshot listing", func(t *testing.T) {
		opened := openStore(t, mf.store)
		if err := opStats(context.Background(), opened.faulty("list", want), nil); !errors.Is(err, want) {
			t.Fatalf("opStats = %v, want %v", err, want)
		}
	})
}

// TestStatsTotalsDoNotList pins the split the census relies on: `stats` reads
// its object and byte totals from the backend's own Stats (which walks for
// physical metadata) and builds its header census from a separate listing walk,
// so a backend whose listing fails still reports the totals half. The listing
// count is asserted, not assumed: it is what tells the two walks apart.
func TestStatsTotalsDoNotList(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "totals")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	want := errors.New("listing failed")
	faulty := newErroringBackend(opened.backend, "list", want)
	st := &store.Store{Backend: faulty, Capabilities: opened.store.Capabilities, Kind: opened.store.Kind}

	if _, err := st.Stats(context.Background()); err != nil {
		t.Fatalf("Stats with a failing listing = %v, want the totals reported", err)
	}
	if faulty.listCalls != 0 {
		t.Fatalf("Stats listed the store %d times, want the physical walk only", faulty.listCalls)
	}
	if err := opStats(context.Background(), st, nil); !errors.Is(err, want) {
		t.Fatalf("opStats = %v, want %v from the census listing", err, want)
	}
	if faulty.listCalls != 1 {
		t.Fatalf("the census listed the store %d times, want exactly its one snapshot walk", faulty.listCalls)
	}
}

// TestListSkipsAnUnreadableObject pins both listing paths' skip: a digest-named
// entry whose header cannot be read is not a listing result. The plain path
// increments the skip counter, and the filtered path — which takes its entries
// from the shared snapshot — does the same, so neither invents a type or a
// codec for it and both warn on stderr while succeeding (cli.md §2).
func TestListSkipsAnUnreadableObject(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "object with an unreadable header")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	st := opened.faulty("get", cas.ErrNotFound)

	for _, args := range [][]string{nil, {"-type", "blob@1"}} {
		var listErr error
		stdout, stderr := captureStreams(t, func() {
			listErr = opList(context.Background(), st, args)
		})
		if listErr != nil {
			t.Fatalf("opList%v over an unreadable object = %v, want it skipped", args, listErr)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Fatalf("opList%v printed %q, want no object lines", args, stdout)
		}
		if !strings.Contains(stderr, "cask: skipped 1 digest-named file(s) that are not readable objects") {
			t.Fatalf("opList%v stderr = %q, want the skipped-file warning", args, stderr)
		}
	}
}

// TestStatsCountsAnUnreadableObject pins the census's `unreadable` axis
// (cli.md §3): an object whose bytes cannot be read is counted there, is
// counted in no header axis, and the summary still reports it as a stored
// object. `sum(types) == objects - unreadable - headerless` therefore still
// holds.
func TestStatsCountsAnUnreadableObject(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "unreadable object")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	st := opened.faulty("get", cas.ErrNotFound)

	out := captureStdout(t, func() {
		if err := opStats(context.Background(), st, nil); err != nil {
			t.Errorf("opStats with an unreadable object: %v", err)
		}
	})
	if !strings.Contains(out, "unreadable: 1") {
		t.Fatalf("stats = %q, want an unreadable: 1 line", out)
	}
	if strings.Contains(out, "types:") {
		t.Fatalf("stats = %q, want no type axis for an unreadable object", out)
	}

	jsonOut := captureStdout(t, func() {
		if err := opStats(context.Background(), st, []string{"-json"}); err != nil {
			t.Errorf("opStats -json with an unreadable object: %v", err)
		}
	})
	var census statsJSON
	if err := json.Unmarshal([]byte(jsonOut), &census); err != nil {
		t.Fatalf("stats -json = %q: %v", jsonOut, err)
	}
	if census.Objects != 1 || census.Unreadable != 1 || census.Headerless != 0 {
		t.Fatalf("stats -json = %+v, want one object counted unreadable", census)
	}
	if len(census.Types) != 0 || len(census.Versions) != 0 || len(census.Codecs) != 0 {
		t.Fatalf("stats -json = %+v, want the unreadable object counted on no header axis", census)
	}
}

// TestMetaMissingObjectIsARuntimeError pins the reporting command's read
// failure: an absent object is a runtime error (1) and no metadata line is
// invented for it (cli.md §3).
func TestMetaMissingObjectIsARuntimeError(t *testing.T) {
	mf := localMF(t)
	missing := sha256.Format(sha256.Of([]byte("never stored")))

	out, stderr, code := runBoth(t, mf, "meta", missing)
	if code != 1 {
		t.Fatalf("meta on an absent object exit = %d, want 1 (runtime)", code)
	}
	if out != "" {
		t.Fatalf("meta on an absent object printed %q, want nothing on stdout", out)
	}
	if !strings.Contains(stderr, "not found") {
		t.Fatalf("meta stderr = %q, want it to report the object as missing", stderr)
	}
}

// TestVerifyAllAbortsOnAReadFailure pins the difference cli.md §2 draws between
// a corrupt object and a read failure: corruption is collected and reported,
// while an object that cannot be read aborts the sweep with the backend's own
// error instead of being counted corrupt.
func TestVerifyAllAbortsOnAReadFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "unreadable during verify")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	want := errors.New("read failed mid-sweep")
	st := opened.faulty("get", want)

	if err := opVerify(context.Background(), st, []string{"--all"}); !errors.Is(err, want) {
		t.Fatalf("verify --all with an unreadable object = %v, want %v", err, want)
	}
}

// TestVerifyUsageBranches pins verify's remaining usage errors: an unknown
// -hash-algo name and a hash the selected algorithm cannot address are usage
// errors (2) rather than a guess or a partial scan (cli.md §2, §4).
func TestVerifyUsageBranches(t *testing.T) {
	mf := localMF(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown algorithm", []string{"--all", "-hash-algo", "nothing"}},
		{"another algorithm's address", []string{"-hash-algo", "sha512", "sha256:" + strings.Repeat("00", 32)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := runBoth(t, mf, "verify", tc.args...); code != 2 {
				t.Fatalf("verify %v exit = %d, want 2 (usage)", tc.args, code)
			}
		})
	}
}

// TestMaintenanceSweepsRejectBadRoots pins the root-hash validation of the two
// sweeps: an unparseable root is a usage error (2) and the sweep never runs, so
// a typo cannot become a sweep with an empty reachable set.
func TestMaintenanceSweepsRejectBadRoots(t *testing.T) {
	mf := localMF(t)
	for _, command := range []string{"gc", "prune"} {
		t.Run(command, func(t *testing.T) {
			if _, _, code := runBoth(t, mf, command, "not-a-digest"); code != 2 {
				t.Fatalf("%s with an invalid root exit = %d, want 2 (usage)", command, code)
			}
		})
	}
}

// TestFilteredListReportsASnapshotFailure pins the filtered listing's runtime
// error: the filter walk needs every object's header, so a store whose listing
// fails fails the whole command (exit 1) instead of returning an empty match
// set an operator could mistake for "no such type".
func TestFilteredListReportsASnapshotFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "header unreadable")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	want := errors.New("header read failed")
	st := opened.faulty("list", want)

	if err := opList(context.Background(), st, []string{"-type", "blob@1"}); !errors.Is(err, want) {
		t.Fatalf("filtered list over a failing snapshot = %v, want %v", err, want)
	}
}

// TestStatsCensusPrintsEveryAxis pins the human-readable census (cli.md §3): one
// line per populated axis with the keys sorted, so two runs over the same store
// print byte-identical text, plus the separate headerless and unreadable
// counters. The authenticated fixture is the census fixture — the preview graph
// carrying the `preview` codec tag plus one raw object written by `put`.
func TestStatsCensusPrintsEveryAxis(t *testing.T) {
	mf := censusFixture(t)

	out := mustRun(t, mf, "stats")
	for _, want := range []string{
		"\ncodecs: preview=8\n",
		"\nheaderless: 1\n",
		"types: blob@1=2, json@1=2, manifest@1=1, note@1=1, text@1=2\n",
		"versions: 2=8\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "unreadable:") {
		t.Fatalf("stats output = %q, want no unreadable line for a fully readable store", out)
	}
}

// TestStatsCensusCountsUnreadableFrames pins the census's `unreadable` axis
// (cli.md §3): when an object's bytes cannot be read it is counted there and is
// guessed into no type, version or codec axis, so a store of unreadable frames
// prints the summary, the unreadable line, and nothing else. The `headerless`
// line is deliberately absent: the unreadable case is decided first, so an
// object the store cannot read is never also described as raw bytes.
func TestStatsCensusCountsUnreadableFrames(t *testing.T) {
	mf := censusFixture(t)
	opened := openStore(t, mf.store)
	st := opened.faulty("get", cas.ErrNotFound)

	out := captureStdout(t, func() {
		if err := opStats(context.Background(), st, nil); err != nil {
			t.Errorf("opStats with unreadable objects: %v", err)
		}
	})
	if !strings.Contains(out, "\nunreadable: 9\n") {
		t.Fatalf("stats = %q, want all nine objects counted unreadable", out)
	}
	if strings.Contains(out, "headerless:") {
		t.Fatalf("stats = %q, must not describe an unreadable object as headerless", out)
	}
	for _, axis := range []string{"types:", "versions:", "codecs:"} {
		if strings.Contains(out, axis) {
			t.Fatalf("stats = %q, want no %s axis when every object is unreadable", out, axis)
		}
	}
}

// TestChecksumVerificationCoversEveryShippedChecksum pins the recorded-checksum
// mode's algorithm switch: each checksum the CLI documents (crc32, adler32,
// crc64) resolves to a hasher and verifies an object recorded with it, and a
// record written by another algorithm is a runtime error naming the mismatch
// rather than corruption (cli.md §4, operations §6).
func TestChecksumVerificationCoversEveryShippedChecksum(t *testing.T) {
	for _, algo := range []struct {
		name   string
		hasher cas.Hasher
	}{
		{crc32.Name, crc32.New()},
		{adler32.Name, adler32.New()},
		{crc64.Name, crc64.New()},
	} {
		t.Run(algo.name, func(t *testing.T) {
			mf := localMF(t)
			backend, err := fs.New(mf.store)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := sidecar.New(backend, sidecar.WithChecksum(algo.name, algo.hasher))
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("payload recorded with " + algo.name)
			d := sha256.Of(data)
			if err := rec.Put(context.Background(), d, bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}

			out, _, code := runBoth(t, mf, "verify", "--checksums", "--checksum", algo.name, sha256.Format(d))
			if code != 0 || !strings.Contains(out, algo.name+" checksum ok") {
				t.Fatalf("verify --checksum %s = (%q, %d), want a %s checksum ok line", algo.name, out, code, algo.name)
			}
		})
	}

	t.Run("a record written by another algorithm", func(t *testing.T) {
		mf := localMF(t)
		d := storedWithChecksum(t, mf.store, []byte("crc32 record, crc64 read"))
		_, stderr, code := runBoth(t, mf, "verify", "--checksums", "--checksum", crc64.Name, sha256.Format(d))
		if code != 1 {
			t.Fatalf("verify --checksums --checksum %s exit = %d, want 1", crc64.Name, code)
		}
		if !strings.Contains(stderr, sidecar.ErrChecksumAlgorithm.Error()) {
			t.Fatalf("stderr = %q, want it to name the recorded-checksum algorithm mismatch", stderr)
		}
	})
}

// TestVerifyChecksumsRejectsAnInvalidHash pins the recorded-checksum mode's hash
// argument: a malformed digest is a usage error (2) before any record is read
// (cli.md §3).
func TestVerifyChecksumsRejectsAnInvalidHash(t *testing.T) {
	mf := localMF(t)
	if _, _, code := runBoth(t, mf, "verify", "--checksums", "not-a-digest"); code != 2 {
		t.Fatalf("verify --checksums not-a-digest exit = %d, want 2 (usage)", code)
	}
}

// TestVerifyChecksumsAllReportsADamagedRecord pins the recorded-checksum
// sweep's error contract (operations §6): a record that cannot be read says the
// pass could not be trusted, so it aborts with the error instead of printing a
// summary — the summary is reserved for a pass whose every record was read.
//
// The other two error exits of the mode are deliberately left uncovered and
// recorded here, because no CLI invocation can reach them: verifyChecksums'
// sidecar.New error (ops.go:669) and reconcileChecksums' sidecar.New error
// (ops.go:745) need a backend that both reports a base path and disagrees with
// the base the record directory is derived from, and reconcileChecksums only
// reaches sidecar.New at all for a backend that DOES report a base path (the
// guard above it). Every backend the CLI can open (fs, packfs) reports one
// base, with no option to override it from the command line
// (testing-strategy §5: an unreachable branch stays uncovered and says so).
func TestVerifyChecksumsAllReportsADamagedRecord(t *testing.T) {
	mf := localMF(t)
	d := storedWithChecksum(t, mf.store, []byte("record will be damaged"))
	record := recordFile(mf.store, d)
	if err := os.WriteFile(record, []byte("{ not a record"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, stderr, code := runBoth(t, mf, "verify", "--checksums", "--all")
	if code != 1 {
		t.Fatalf("verify --checksums --all with a damaged record exit = %d, want 1 (stderr %q)", code, stderr)
	}
	if strings.Contains(out, "checked ") {
		t.Fatalf("stdout = %q, want no summary for a pass that could not read a record", out)
	}
	if !strings.Contains(stderr, "record") {
		t.Fatalf("stderr = %q, want it to name the damaged record", stderr)
	}
	if _, _, code := runBoth(t, mf, "verify", "--checksums", sha256.Format(d)); code != 1 {
		t.Fatalf("verify --checksums on the same damaged record exit = %d, want 1", code)
	}
}

// TestLocalPutReportsASpoolCreationFailure pins the write path's first step: a
// temp directory the process cannot create a spool file in is reported rather
// than silently skipped, because hashing needs somewhere to rewind (ops.go).
// The temp directory is process-wide and std-lib-defined, so pointing it at a
// path that cannot exist is the deterministic seam — every name the standard
// library consults is set, TMPDIR on Unix and TMP/TEMP on Windows.
func TestLocalPutReportsASpoolCreationFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "sub")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, missing)
	}
	_, _, err := localPut(context.Background(), backmem.New(), strings.NewReader("payload"))
	if err == nil {
		t.Fatal("localPut without a usable temp directory = nil, want an error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want the filesystem's own missing-directory error", err)
	}
}

// TestStoreCommandsReportAnInjectedSizeFailure pins how the two reporting
// commands treat the same failed metadata read (cli.md §2, §3): `meta` is about
// that one object, so it fails (exit 1) with the backend's error; `stats` is a
// census, so it succeeds and counts the object on its `unreadable` axis instead
// of inventing a size for it.
func TestStoreCommandsReportAnInjectedSizeFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "metadata read fails")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	digests, err := opened.backend.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("fixture lists %d objects, want 1", len(digests))
	}
	address := sha256.Format(digests[0])
	want := errors.New("size failed")
	st := opened.faulty("size", want)

	if err := opMeta(context.Background(), st, []string{address}); !errors.Is(err, want) {
		t.Fatalf("opMeta with a failing Size = %v, want %v", err, want)
	}

	var statsErr error
	out := captureStdout(t, func() { statsErr = opStats(context.Background(), st, nil) })
	if statsErr != nil {
		t.Fatalf("opStats with a failing Size = %v, want the census to count it unreadable", statsErr)
	}
	if !strings.Contains(out, "1 objects") || !strings.Contains(out, "\nunreadable: 1\n") {
		t.Fatalf("stats = %q, want the object counted and reported unreadable", out)
	}

	// The same read through the dispatcher is a runtime error carrying the
	// backend's message.
	var code int
	_, reported := captureStreams(t, func() { code = reportError(opMeta(context.Background(), st, []string{address})) })
	if code != 1 || !strings.Contains(reported, want.Error()) {
		t.Fatalf("meta classification = (%d, %q), want exit 1 and the backend's message", code, reported)
	}
}

// TestListRuntimeErrorsAreReportedThroughTheCLI pins the listing's two runtime
// failures at the CLI boundary (cli.md §3): a store whose listing fails, and an
// entry whose metadata read fails after it was listed. Both are reported as
// runtime errors (exit 1) with the backend's message on stderr, and neither
// prints an object line on stdout.
func TestListRuntimeErrorsAreReportedThroughTheCLI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault string
	}{
		{"listing fails", "list"},
		{"entry metadata fails", "size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mf := localMF(t)
			if _, code := run(t, mf, "put", writeTemp(t, "listed object")); code != 0 {
				t.Fatal("put failed")
			}
			opened := openStore(t, mf.store)
			want := errors.New(tc.name + ": injected")
			st := opened.faulty(tc.fault, want)

			// The operation returns the backend's error and prints nothing:
			// reporting the failure is its caller's job.
			spec, ok := command("list")
			if !ok {
				t.Fatal("list is not a registered command")
			}
			var opErr error
			stdout, stderr := captureStreams(t, func() {
				opErr = spec.op(context.Background(), st, nil)
			})
			if !errors.Is(opErr, want) {
				t.Fatalf("list over the faulty store = %v, want %v", opErr, want)
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("list wrote to the streams (stdout %q, stderr %q), want the caller to report the error", stdout, stderr)
			}

			// The dispatcher's classification turns the same failure into exit
			// 1 with the message on stderr.
			var code int
			_, reported := captureStreams(t, func() { code = reportError(opErr) })
			if code != 1 {
				t.Fatalf("reportError(%v) = %d, want 1 (runtime)", opErr, code)
			}
			if !strings.Contains(reported, want.Error()) {
				t.Fatalf("stderr = %q, want it to carry the backend's message", reported)
			}
		})
	}
}
func TestReconcileChecksumsSkipsABackendWithoutABase(t *testing.T) {
	st := &store.Store{Backend: backmem.New(), Kind: store.KindFS}
	if err := reconcileChecksums(context.Background(), st, "gc"); err != nil {
		t.Fatalf("reconcileChecksums over a backend with no base = %v, want nil", err)
	}
}

// TestPutReadsStandardInputForADashOperand pins the piped-input path cli.md §2
// documents: `put -` stores the bytes on stdin instead of a named file, reports
// the same printable digest a file put would, and a second identical run reports
// the object as deduplicated. Operand arity is still enforced.
func TestPutReadsStandardInputForADashOperand(t *testing.T) {
	input := filepath.Join(t.TempDir(), "stdin.bin")
	if err := os.WriteFile(input, []byte("bytes from stdin"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	oldStdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = oldStdin }()

	mf := localMF(t)
	out, code := run(t, mf, "put", "-")
	if code != 0 {
		t.Fatalf("put - exit = %d, want 0", code)
	}
	hash := strings.TrimSpace(out)
	if hash != sha256.Format(sha256.Of([]byte("bytes from stdin"))) {
		t.Fatalf("put - printed %q, want the digest of the input bytes", hash)
	}
	if got, code := run(t, mf, "get", hash); code != 0 || got != "bytes from stdin" {
		t.Fatalf("get of the stdin object = (%q, %d), want the stored bytes", got, code)
	}

	// A second run over the same bytes is a no-op that says so.
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	out, code = run(t, mf, "put", "-")
	if code != 0 {
		t.Fatalf("second put - exit = %d, want 0", code)
	}
	if !strings.Contains(out, "(deduplicated)") {
		t.Fatalf("second put - printed %q, want the deduplicated report", out)
	}

	if _, _, code := runBoth(t, mf, "put", "-", "extra"); code != 2 {
		t.Fatalf("put - extra exit = %d, want 2 (usage)", code)
	}
}

// TestVersionCommandWithoutArguments pins the version subcommand's successful
// shape: it takes no operands, prints the library and Go version lines, and
// exits 0 — while an operand is the usage error its table entry documents
// (cli.md §2, §3).
func TestVersionCommandWithoutArguments(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runVersionCommand(context.Background(), modeFlags{}, nil); code != 0 {
			t.Errorf("runVersionCommand exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "cask ") || !strings.Contains(out, "go ") {
		t.Fatalf("version output = %q, want the cask and go lines", out)
	}

	// The command takes no operands: one is the usage error its own table entry
	// documents (exit 2), reported before anything is printed.
	var code int
	stdout, stderr := captureStreams(t, func() {
		code = runVersionCommand(context.Background(), modeFlags{}, []string{"extra"})
	})
	if code != 2 {
		t.Fatalf("version extra exit = %d, want 2 (usage)", code)
	}
	if stdout != "" {
		t.Fatalf("version extra printed %q on stdout, want nothing", stdout)
	}
	if !strings.Contains(stderr, "version takes no arguments") {
		t.Fatalf("version extra stderr = %q, want the operand rejection", stderr)
	}
}

// TestSweepsAndCensusReportBackendFailures pins the runtime error of the
// commands that read the whole store at once (cli.md §3): `gc` (through
// cas.Sweep) and `stats` (through the backend's own totals) both report the
// backend's error, so a failed sweep can never be printed as "deleted 0
// objects".
func TestSweepsAndCensusReportBackendFailures(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "sweep and census")); code != 0 {
		t.Fatal("put failed")
	}
	root := sha256.Format(sha256.Of([]byte("sweep and census")))

	t.Run("gc", func(t *testing.T) {
		opened := openStore(t, mf.store)
		want := errors.New("sweep failed")
		st := opened.faulty("list", want)
		if err := opGC(context.Background(), st, []string{"--min-age", "0", root}); !errors.Is(err, want) {
			t.Fatalf("opGC with a failing sweep = %v, want %v", err, want)
		}
	})

	t.Run("stats", func(t *testing.T) {
		opened := openStore(t, mf.store)
		want := errors.New("stats failed")
		st := opened.faulty("stats", want)
		if err := opStats(context.Background(), st, nil); !errors.Is(err, want) {
			t.Fatalf("opStats with failing totals = %v, want %v", err, want)
		}
	})
}

// TestGcWithoutRootsIsAUsageError pins the reachable-root guard: `gc` run with no
// operand has no reachable set to preserve, so it is refused (exit 2) before any
// sweep instead of deleting the whole store (cli.md §2, §3).
func TestGcWithoutRootsIsAUsageError(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "must survive")); code != 0 {
		t.Fatal("put failed")
	}
	_, stderr, code := runBoth(t, mf, "gc", "--min-age", "0")
	if code != 2 {
		t.Fatalf("gc without roots exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "gc needs at least one root hash") {
		t.Fatalf("stderr = %q, want the root requirement named", stderr)
	}
	// Nothing was swept: the guard runs before the store is touched.
	if _, code := run(t, mf, "list"); code != 0 {
		t.Fatal("the store is unreadable after a refused gc")
	}
}

// TestVerifyReportsACorruptObject pins the single-object integrity check's
// failure (cli.md §2): an object whose bytes no longer match its address is a
// runtime error (exit 1) naming the digest mismatch, and no "ok" line is
// printed for it.
func TestVerifyReportsACorruptObject(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "bytes that will be replaced")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	digests, err := opened.backend.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 {
		t.Fatalf("fixture lists %d objects, want 1", len(digests))
	}
	address := sha256.Format(digests[0])
	// Overwrite the object file with different bytes of the same digest name.
	tampered := []byte("these bytes are not the ones that were stored")
	if err := os.WriteFile(fsObjectPath(mf.store, digests[0]), tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runBoth(t, mf, "verify", address)
	if code != 1 {
		t.Fatalf("verify over a corrupt object exit = %d, want 1 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("verify stdout = %q, want no ok line for a corrupt object", stdout)
	}
	if !strings.Contains(stderr, "digest mismatch") {
		t.Fatalf("stderr = %q, want the digest mismatch reported", stderr)
	}
}

// TestListReportsAHeaderReadFailure pins the unfiltered listing's read failure:
// an entry whose header cannot be read at all fails the command (exit 1) instead
// of being printed from metadata that was never obtained, while an entry the
// snapshot merely marks unreadable is skipped (TestListSkipsAnUnreadableObject).
func TestListReportsAHeaderReadFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "header unreadable")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	want := errors.New("header read failed")
	st := opened.faulty("get", want)

	if err := opList(context.Background(), st, nil); !errors.Is(err, want) {
		t.Fatalf("opList over a failing header read = %v, want %v", err, want)
	}
}

// TestSeedPreviewReportsASeedFailure pins the seed command's error exit: a store
// that cannot record the preview objects fails the command (exit 1) with the
// wrapped ordinal, instead of reporting a partial seed as success.
func TestSeedPreviewReportsASeedFailure(t *testing.T) {
	mf := localMF(t)
	opened := openStore(t, mf.store)
	want := errors.New("seed write failed")
	st := opened.faulty("put", want)

	var opErr error
	stdout, _ := captureStreams(t, func() {
		opErr = opSeedPreview(context.Background(), st, []string{"-count", "4"})
	})
	if !errors.Is(opErr, want) {
		t.Fatalf("opSeedPreview with a failing store = %v, want %v", opErr, want)
	}
	if stdout != "" {
		t.Fatalf("opSeedPreview printed %q, want the partial-seed report withheld", stdout)
	}

	var code int
	_, reported := captureStreams(t, func() { code = reportError(opErr) })
	if code != 1 {
		t.Fatalf("seed-preview classification = %d, want 1 (runtime)", code)
	}
	if !strings.Contains(reported, "seed preview object 0") {
		t.Fatalf("stderr = %q, want the failing ordinal named", reported)
	}
}

// TestReconcileChecksumsReportsAReadFailure pins the reconciliation error path:
// a record sweep whose store listing fails reports the backend's error instead
// of printing a fabricated summary. The failure is injected below the sidecar
// layer, because a record directory needs an explicit base path for a backend
// that cannot report one (cas/verify/sidecar), and the store under test is the
// sidecar backend itself — what `cask gc`/`cask prune` run their reconciliation
// over once a store has records.
func TestReconcileChecksumsReportsAReadFailure(t *testing.T) {
	mf := localMF(t)
	opened := openStore(t, mf.store)
	want := errors.New("listing failed during reconciliation")
	rec, err := sidecar.New(newErroringBackend(opened.backend, "list", want))
	if err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Backend: rec, Kind: store.KindFS}

	if err := reconcileChecksums(context.Background(), st, "gc"); !errors.Is(err, want) {
		t.Fatalf("reconcileChecksums with a failing listing = %v, want %v", err, want)
	}
}

// TestPruneDestructiveReportsASweepFailure pins the destructive prune's runtime
// error: a sweep that cannot run is reported (exit 1) and no summary line
// claims objects were deleted.
func TestPruneDestructiveReportsASweepFailure(t *testing.T) {
	mf := localMF(t)
	if _, code := run(t, mf, "put", writeTemp(t, "sweep me")); code != 0 {
		t.Fatal("put failed")
	}
	opened := openStore(t, mf.store)
	want := errors.New("sweep failed")
	st := opened.faulty("list", want)

	root := sha256.Format(sha256.Of([]byte("sweep me")))
	if err := opPrune(context.Background(), st, []string{"--min-age", "0", "--dry-run=false", root}); !errors.Is(err, want) {
		t.Fatalf("prune --dry-run=false with a failing sweep = %v, want %v", err, want)
	}
}

// TestMaintenanceFlagErrorsAreUsageErrors pins the flag parsing of the
// maintenance commands and of verify: an unknown flag is a usage error (2)
// before any store state is touched (cli.md §3, §4).
func TestMaintenanceFlagErrorsAreUsageErrors(t *testing.T) {
	mf := localMF(t)
	root := "sha256:" + strings.Repeat("00", 32)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"gc", []string{"gc", "-nope", root}},
		{"prune", []string{"prune", "-nope", root}},
		{"clean", []string{"clean", "-nope"}},
		{"verify", []string{"verify", "-nope", root}},
		{"checksum sweep", []string{"verify", "--checksums", "-nope", root}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := runBoth(t, mf, tc.args[0], tc.args[1:]...); code != 2 {
				t.Fatalf("%v exit = %d, want 2 (usage)", tc.args, code)
			}
		})
	}
}
