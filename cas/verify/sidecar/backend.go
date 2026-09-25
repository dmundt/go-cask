package sidecar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
)

const (
	// metaDirName is the directory inside a store's base that holds the
	// records. Its files are neither digest-named nor left without the `.json`
	// suffix, which is what keeps them out of a backend's List and Stats; the
	// `.tmp` scratch files a record write creates are reclaimed by the
	// backend's Clean like any other temp file.
	metaDirName = ".meta"
	// recordSuffix ends every record file name.
	recordSuffix = ".json"
	// defaultPrefixBytes bounds how much of an object is captured so the
	// record's optional type and codec fields can be derived without buffering
	// the object.
	defaultPrefixBytes = 4096
	// dirPerm and filePerm match the store's own permissions (defaults §4).
	dirPerm  = 0o755
	filePerm = 0o644
)

// Backend decorates a cas.Backend with an optional per-object checksum record.
// It is a cas.Backend itself, so it composes wherever one is accepted:
//
//	rec, err := sidecar.New(backend,
//		sidecar.WithBase("store/objects"),
//		sidecar.WithChecksum(crc32.Name, crc32.New()))
//	store := cas.New(rec, json.New[*Blob](), sha256.New())
//
// Put stores the object and then records a checksum of the stored bytes; Delete
// removes the object and its record; every read path (Get, Exists, List, Stats)
// delegates untouched. A Backend holds no objects: deleting the record
// directory loses the cheap check, never an object.
type Backend struct {
	inner          cas.Backend
	base           string
	metaDir        string
	algo           string
	hasher         cas.Hasher
	dirSync        bool
	maxRecordBytes int64
}

// A Backend is a cas.Backend: the decorator's whole point is that a caller can
// hand it to anything that already accepts one.
var _ cas.Backend = (*Backend)(nil)

// New wraps inner with a sidecar record store.
//
// The base comes from WithBase or, when the backend reports one, from its
// BasePath: the directory its object bytes live under, which is where the
// records go. A WithBase that disagrees with the backend's own base path is
// refused — records beside one store's bytes must not describe another store's
// objects.
//
// WithChecksum configures the write path. Without it the decorator delegates
// Put unchanged and records nothing, and only the read paths (Load, Keys,
// Verifier, Reconcile) are usable.
func New(inner cas.Backend, options ...Option) (*Backend, error) {
	if inner == nil {
		return nil, fmt.Errorf("sidecar: nil backend")
	}
	cfg := config{maxRecordBytes: DefaultMaxRecordBytes}
	for _, option := range options {
		if option == nil {
			continue
		}
		option(&cfg)
	}
	if (cfg.hasher == nil) != (cfg.algo == "") {
		return nil, fmt.Errorf("sidecar: WithChecksum needs both an algorithm name and a hasher")
	}
	base, err := resolveBase(inner, &cfg)
	if err != nil {
		return nil, err
	}
	return &Backend{
		inner:          inner,
		base:           base,
		metaDir:        filepath.Join(base, metaDirName),
		algo:           cfg.algo,
		hasher:         cfg.hasher,
		dirSync:        cfg.dirSync,
		maxRecordBytes: cfg.maxRecordBytes,
	}, nil
}

// BasePath returns the base directory the records live under. It is the same
// structural interface fs.Backend and packfs.Backend report, so a decorator
// stacked on this one resolves its base without WithBase and cannot silently
// point at a different store.
func (b *Backend) BasePath() string { return b.base }

// Put stores the object through the inner backend and, when the digest has no
// usable record under this writer's algorithm, records a checksum of the stored
// bytes.
//
// Without WithChecksum there is nothing to record, so Put delegates unchanged
// and writes no record.
//
// A repeat Put whose record already carries this algorithm is left untouched, so
// the record stays deterministic and its creation time is not refreshed. A
// record that exists but cannot be read, or that names another algorithm, is
// rewritten: the object is the store's data, the record is derived metadata. A
// write that the inner backend does not read to EOF returns an error without a
// record, because a checksum over partial bytes is worse than none.
func (b *Backend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "sidecar: put"); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("sidecar: put %s: nil reader", d)
	}
	if b.hasher == nil {
		return b.inner.Put(ctx, d, r)
	}
	existing, found, err := b.readRecord(d)
	if err != nil {
		// Damage to a derived file never blocks the store's own write: the
		// record is rewritten below, and Verify reports the object's state.
		found = false
	}
	if found && existing.ChecksumAlgo == b.algo {
		return b.inner.Put(ctx, d, r)
	}

	reader := &eofReader{r: r}
	if err := b.inner.Put(ctx, d, reader); err != nil {
		return err
	}
	if !reader.eof {
		return fmt.Errorf("sidecar: put %s: the backend returned before reading the object to EOF; no record written", d)
	}

	sum, size, prefix, err := b.storedChecksum(ctx, d)
	if err != nil {
		return err
	}
	rec := &Record{
		Version:      RecordVersion,
		Digest:       d,
		Type:         envelopeType(prefix),
		Codec:        envelopeCodec(prefix),
		ChecksumAlgo: b.algo,
		Checksum:     sum,
		Size:         size,
		CreatedAt:    time.Now().UTC(),
	}
	if found {
		rec.CreatedAt = existing.CreatedAt
	}
	return b.writeRecord(ctx, rec)
}

// Get returns the object's bytes, unread and unchecked: a record is a separate
// maintenance signal, never a read-path gate.
func (b *Backend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}

// Exists reports whether the object is stored. A record does not affect it: an
// object without a record exists, and a record without an object does not.
func (b *Backend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}

// Delete removes the object and then its record. A missing object stays a no-op
// and a missing record is not an error, so the method is idempotent exactly as
// the Backend contract requires.
func (b *Backend) Delete(ctx context.Context, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "sidecar: delete"); err != nil {
		return err
	}
	if err := b.inner.Delete(ctx, d); err != nil {
		return err
	}
	if err := os.Remove(b.recordPath(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sidecar: delete record %s: %w", d, err)
	}
	return nil
}

// List returns the inner backend's digests, unchanged: record files are not
// objects and never appear here.
func (b *Backend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

// Stats returns the inner backend's object and byte totals, unchanged: a record
// is not an object and is not counted.
func (b *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

// Load returns the record stored for d. A missing record reports ErrUnrecorded,
// which wraps cas.ErrNotFound — an object without a record is not corruption, it
// is simply unchecked — while a record that exists and cannot be read is
// cas.ErrCorrupt, never a silent skip.
func (b *Backend) Load(ctx context.Context, d cas.Digest) (*Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cas.CheckDigest(d, "sidecar: load"); err != nil {
		return nil, err
	}
	rec, found, err := b.readRecord(d)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrUnrecorded, d)
	}
	return rec, nil
}

// Keys returns the digests that have a record, sorted by their hex form. Keys
// reads names, not contents: use Load for a validated record.
func (b *Backend) Keys(ctx context.Context) ([]cas.Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(b.metaDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sidecar: list records: %w", err)
	}
	keys := make([]cas.Digest, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, recordSuffix) {
			continue
		}
		d, err := cas.ParseDigest(strings.TrimSuffix(name, recordSuffix))
		if err != nil {
			return nil, fmt.Errorf("%w: sidecar: record file %s: %v", cas.ErrCorrupt, name, err)
		}
		keys = append(keys, d)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys, nil
}

// ReconcileReport summarizes a Reconcile pass: how many records it examined,
// the records it removed because their object is gone, and the stored objects
// that have no record.
type ReconcileReport struct {
	// Records is the number of records examined.
	Records int
	// Removed lists digests whose record was removed because the object is no
	// longer stored.
	Removed []cas.Digest
	// Unrecorded lists stored objects that have no record. They are reported,
	// never treated as damaged, and never given a record: only a writer that
	// read the bytes can compute one.
	Unrecorded []cas.Digest
}

// Reconcile closes the gap a crash or an out-of-band change can leave behind:
// it removes records whose object is gone and reports objects without a record.
// It never deletes an object and never writes a record.
//
// A sweep deletes objects, so this is what keeps the record directory from
// accumulating orphans after `cask gc` or `cask prune`; it is idempotent and
// cheap (one existence check per record plus one list).
func (b *Backend) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys, err := b.Keys(ctx)
	if err != nil {
		return nil, err
	}
	report := &ReconcileReport{Records: len(keys)}
	recorded := make(map[string]struct{}, len(keys))
	for _, d := range keys {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		recorded[d.String()] = struct{}{}
		exists, err := b.inner.Exists(ctx, d)
		if err != nil {
			return report, err
		}
		if exists {
			continue
		}
		if err := os.Remove(b.recordPath(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return report, fmt.Errorf("sidecar: reconcile: remove record %s: %w", d, err)
		}
		report.Removed = append(report.Removed, d)
	}
	digests, err := b.inner.List(ctx)
	if err != nil {
		return report, err
	}
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if _, ok := recorded[d.String()]; !ok {
			report.Unrecorded = append(report.Unrecorded, d)
		}
	}
	return report, nil
}

// recordPath is the file one object's record lives in: the digest's hex form
// plus the `.json` suffix that keeps it out of a digest scan.
func (b *Backend) recordPath(d cas.Digest) string {
	return filepath.Join(b.metaDir, d.String()+recordSuffix)
}

// readRecord loads one record. found is false when no record file exists; a
// record that exists but cannot be read, is oversized, or names another digest
// is cas.ErrCorrupt, because a record is either usable or damage. A record is
// derived metadata whose only source of truth is the store's own bytes, so one
// that cannot be trusted is corruption rather than a transient I/O failure —
// which is what Load and Verify document. Every arm keeps its cause on the
// chain, so an operator still sees the underlying open or read error.
func (b *Backend) readRecord(d cas.Digest) (rec *Record, found bool, err error) {
	f, err := os.Open(b.recordPath(d))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: sidecar: open record %s: %w", cas.ErrCorrupt, d, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, b.maxRecordBytes+1))
	if err != nil {
		return nil, true, fmt.Errorf("%w: sidecar: read record %s: %w", cas.ErrCorrupt, d, err)
	}
	if int64(len(data)) > b.maxRecordBytes {
		return nil, true, fmt.Errorf("%w: sidecar: record for %s exceeds %d bytes", cas.ErrCorrupt, d, b.maxRecordBytes)
	}
	rec, err = decodeRecord(data)
	if err != nil {
		return nil, true, err
	}
	if !rec.Digest.Equal(d) {
		return nil, true, fmt.Errorf("%w: sidecar: record for %s names %s", cas.ErrCorrupt, d, rec.Digest)
	}
	return rec, true, nil
}

// writeRecord publishes a record atomically: a temp file in the record
// directory, fsynced, then renamed into place. The temp name ends in `.tmp`, so
// a crash between create and rename leaves something the backend's Clean
// reclaims.
func (b *Backend) writeRecord(ctx context.Context, rec *Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := rec.encode()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(b.metaDir, dirPerm); err != nil {
		return fmt.Errorf("sidecar: create record directory: %w", err)
	}
	f, err := os.CreateTemp(b.metaDir, rec.Digest.String()+".*.tmp")
	if err != nil {
		return fmt.Errorf("sidecar: create record temp: %w", err)
	}
	tmp := f.Name()
	if err := f.Chmod(filePerm); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sidecar: set record permissions %s: %w", rec.Digest, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sidecar: write record %s: %w", rec.Digest, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sidecar: sync record %s: %w", rec.Digest, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sidecar: close record %s: %w", rec.Digest, err)
	}
	if err := os.Rename(tmp, b.recordPath(rec.Digest)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sidecar: publish record %s: %w", rec.Digest, err)
	}
	if !b.dirSync {
		return nil
	}
	return syncDir(b.metaDir)
}

// syncDir fsyncs the record directory after a rename, so the rename itself
// survives a crash. Windows has no directory fsync — the handle cannot be
// opened for it — so this is a no-op there, exactly as the filesystem backend's
// own directory sync is (cas/backend/fs, syncParentDir).
func syncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("sidecar: open record directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sidecar: sync record directory: %w", err)
	}
	return nil
}

// storedChecksum re-reads the stored object and returns its checksum, its size
// and the captured prefix the optional record fields are derived from. It is
// one streaming pass over the stored bytes — exactly what Get returns — so the
// recorded checksum describes the store's copy, not the writer's intent.
func (b *Backend) storedChecksum(ctx context.Context, d cas.Digest) (cas.Digest, int64, []byte, error) {
	rc, err := b.inner.Get(ctx, d)
	if err != nil {
		return nil, 0, nil, err
	}
	captured := &prefixReader{r: rc, prefix: make([]byte, 0, defaultPrefixBytes)}
	sum, err := b.hasher.Digest(captured)
	closeErr := rc.Close()
	if err != nil {
		return nil, 0, nil, fmt.Errorf("sidecar: checksum %s: %w", d, err)
	}
	if closeErr != nil {
		return nil, 0, nil, fmt.Errorf("sidecar: checksum %s: close: %w", d, closeErr)
	}
	return sum, captured.n, captured.prefix, nil
}

// prefixReader records how many bytes passed through and captures the first
// defaultPrefixBytes of them, so the envelope header can be inspected without
// buffering the object.
type prefixReader struct {
	r      io.Reader
	prefix []byte
	n      int64
}

func (p *prefixReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if room := defaultPrefixBytes - len(p.prefix); room > 0 && n > 0 {
		p.prefix = append(p.prefix, b[:min(room, n)]...)
	}
	return n, err
}

// eofReader reports whether the reader it wraps was read to EOF — the guard
// against a backend that returns after a partial read, which would make a
// recorded checksum describe bytes nobody stored.
type eofReader struct {
	r   io.Reader
	eof bool
}

func (e *eofReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		e.eof = true
	}
	return n, err
}

// envelopeType and envelopeCodec read the optional record fields from the
// captured prefix. Both are best effort: bytes that are not a go-cask envelope,
// or an object larger than the captured prefix, leave the field empty rather
// than failing a write.
func envelopeType(prefix []byte) string {
	if env, err := cas.EnvelopeFromBytes(prefix); err == nil {
		return env.Type
	}
	if name, err := cas.EnvelopeType(prefix); err == nil {
		return name
	}
	return ""
}

// envelopeCodec reads the codec identity tag. The exported envelope readers do
// not expose the header alone, so a tag is recorded only when the whole
// envelope fits the captured prefix; Type is derived from the header either way.
func envelopeCodec(prefix []byte) string {
	if env, err := cas.EnvelopeFromBytes(prefix); err == nil {
		return env.Codec
	}
	return ""
}
