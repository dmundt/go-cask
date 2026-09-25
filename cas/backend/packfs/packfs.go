package packfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
)

var errInvalidPackRecord = errors.New("invalid pack record")

// config is the pack backend's own configuration. Option is a func over this
// concrete type, so only packfs options can configure packfs.
type config struct {
	enabled        bool
	packMaxBytes   int64
	packMaxEntries int
}

// Option configures a packfs Backend.
type Option func(*config)

// WithEnabled turns packfile mode on. Packfiles are intentionally opt-in.
func WithEnabled() Option {
	return func(c *config) { c.enabled = true }
}

// WithPackMaxBytes rotates the active pack when the append-only file reaches the
// configured size. 0 means unlimited.
func WithPackMaxBytes(maxBytes int64) Option {
	return func(c *config) { c.packMaxBytes = maxBytes }
}

// WithPackMaxEntries rotates the active pack once the in-memory index grows past
// the configured count. 0 means unlimited.
func WithPackMaxEntries(maxEntries int) Option {
	return func(c *config) { c.packMaxEntries = maxEntries }
}

type packRecord struct {
	// Pack identifies the pack file.
	Pack string `json:"pack"`
	// Offset is the payload start in Pack.
	Offset int64 `json:"offset"`
	// Size is the payload length.
	Size int64 `json:"size"`
}

// manifest is the on-disk pack index: one record per packed digest.
//
// Entries is keyed by the digest's lowercase-hex form, NOT by its raw bytes, and
// that is load-bearing: encoding/json replaces invalid UTF-8 in a string (a map
// key included) with U+FFFD, and a digest is arbitrary binary. A manifest
// written with raw digest keys therefore reads back with corrupted keys — the
// index would hold keys no lookup can match and no List should report. Only the
// encoding differs: the in-memory index stays keyed by the raw digest bytes
// (string(d)), which is what every lookup uses.
type manifest struct {
	// Entries maps each packed digest's hex form to its pack location.
	Entries map[string]packRecord `json:"entries"`
}

// ops holds every filesystem and loose-backend operation a pack backend
// performs. New installs the real implementations (realOps); a nil field falls
// back to the real one, so a zero-value ops — and a Backend built directly by a
// test — still behaves correctly. The seams live on the struct rather than in
// package-level variables, so two backends in one process cannot interfere and
// tests may run in parallel.
type ops struct {
	mkdirAll   func(string, os.FileMode) error
	openFile   func(string, int, os.FileMode) (*os.File, error)
	readFile   func(string) ([]byte, error)
	writeFile  func(string, []byte, os.FileMode) error
	rename     func(string, string) error
	open       func(string) (*os.File, error)
	createTemp func(string, string) (*os.File, error)

	loosePut    func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest, r io.Reader) error
	looseGet    func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (io.ReadCloser, error)
	looseExists func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (bool, error)
	looseDelete func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) error
	looseList   func(ctx context.Context, loose *fsbackend.Backend) ([]cas.Digest, error)
	looseStats  func(ctx context.Context, loose *fsbackend.Backend) (*cas.Stats, error)
}

// realOps returns the operations a pack backend uses in production.
func realOps() ops {
	return ops{
		mkdirAll:   os.MkdirAll,
		openFile:   os.OpenFile,
		readFile:   os.ReadFile,
		writeFile:  os.WriteFile,
		rename:     os.Rename,
		open:       os.Open,
		createTemp: os.CreateTemp,
		loosePut: func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest, r io.Reader) error {
			return loose.Put(ctx, d, r)
		},
		looseGet: func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (io.ReadCloser, error) {
			return loose.Get(ctx, d)
		},
		looseExists: func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (bool, error) {
			return loose.Exists(ctx, d)
		},
		looseDelete: func(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) error { return loose.Delete(ctx, d) },
		looseList:   func(ctx context.Context, loose *fsbackend.Backend) ([]cas.Digest, error) { return loose.List(ctx) },
		looseStats:  func(ctx context.Context, loose *fsbackend.Backend) (*cas.Stats, error) { return loose.Stats(ctx) },
	}
}

// mkdirAll creates dir and any missing parent, or calls the installed seam.
func (o ops) mkdirAllDo(dir string, perm os.FileMode) error {
	if o.mkdirAll == nil {
		return os.MkdirAll(dir, perm)
	}
	return o.mkdirAll(dir, perm)
}

// openFileDo opens or creates a file, or calls the installed seam.
func (o ops) openFileDo(name string, flag int, perm os.FileMode) (*os.File, error) {
	if o.openFile == nil {
		return os.OpenFile(name, flag, perm)
	}
	return o.openFile(name, flag, perm)
}

// readFileDo reads a whole file, or calls the installed seam.
func (o ops) readFileDo(name string) ([]byte, error) {
	if o.readFile == nil {
		return os.ReadFile(name)
	}
	return o.readFile(name)
}

// writeFileDo writes a whole file, or calls the installed seam.
func (o ops) writeFileDo(name string, data []byte, perm os.FileMode) error {
	if o.writeFile == nil {
		return os.WriteFile(name, data, perm)
	}
	return o.writeFile(name, data, perm)
}

// renameDo renames a file, or calls the installed seam.
func (o ops) renameDo(oldPath, newPath string) error {
	if o.rename == nil {
		return os.Rename(oldPath, newPath)
	}
	return o.rename(oldPath, newPath)
}

// openDo opens a file for reading, or calls the installed seam.
func (o ops) openDo(name string) (*os.File, error) {
	if o.open == nil {
		return os.Open(name)
	}
	return o.open(name)
}

// createTempDo creates a scratch file, or calls the installed seam.
func (o ops) createTempDo(dir, pattern string) (*os.File, error) {
	if o.createTemp == nil {
		return os.CreateTemp(dir, pattern)
	}
	return o.createTemp(dir, pattern)
}

// putDo, getDo, existsDo, deleteDo, listDo and statsDo forward to the installed
// loose-backend seam.
func (o ops) putDo(ctx context.Context, loose *fsbackend.Backend, d cas.Digest, r io.Reader) error {
	if o.loosePut == nil {
		return loose.Put(ctx, d, r)
	}
	return o.loosePut(ctx, loose, d, r)
}

func (o ops) getDo(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (io.ReadCloser, error) {
	if o.looseGet == nil {
		return loose.Get(ctx, d)
	}
	return o.looseGet(ctx, loose, d)
}

func (o ops) existsDo(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) (bool, error) {
	if o.looseExists == nil {
		return loose.Exists(ctx, d)
	}
	return o.looseExists(ctx, loose, d)
}

func (o ops) deleteDo(ctx context.Context, loose *fsbackend.Backend, d cas.Digest) error {
	if o.looseDelete == nil {
		return loose.Delete(ctx, d)
	}
	return o.looseDelete(ctx, loose, d)
}

func (o ops) listDo(ctx context.Context, loose *fsbackend.Backend) ([]cas.Digest, error) {
	if o.looseList == nil {
		return loose.List(ctx)
	}
	return o.looseList(ctx, loose)
}

func (o ops) statsDo(ctx context.Context, loose *fsbackend.Backend) (*cas.Stats, error) {
	if o.looseStats == nil {
		return loose.Stats(ctx)
	}
	return o.looseStats(ctx, loose)
}

// Backend is a filesystem-backed pack extension. It stores objects in a loose
// backend and optionally mirrors them into an append-only pack file plus a small
// JSON index that points to the payload offset within the pack.
type Backend struct {
	mu           sync.Mutex
	base         string
	loose        *fsbackend.Backend
	manifestPath string
	packDir      string
	packFilePath string
	packFile     *os.File
	packBytes    int64
	packEntries  int
	cfg          config
	index        map[string]packRecord
	op           ops
}

var _ cas.Backend = (*Backend)(nil)
var _ cas.Cleaner = (*Backend)(nil)
var _ cas.Statter = (*Backend)(nil)
var _ cas.BatchGetter = (*Backend)(nil)

// New creates a pack-backed filesystem backend. It stays opt-in: without
// WithEnabled, the backend behaves like the regular loose fs backend.
//
// Like the loose backend it wraps, and before it creates anything, it checks
// basePath with fs.ValidateBase: the base owns its loose tree, its pack
// directory and its index exclusively (fs.List/Stats/Clean work beneath it), so
// an empty path, ".", "..", a parent-traversal path or a volume root is
// rejected.
func New(basePath string, opts ...Option) (*Backend, error) {
	return newWithOps(basePath, realOps(), opts...)
}

// newWithOps is New with the operation seams supplied, so in-package tests can
// exercise construction failures without package-level state.
func newWithOps(basePath string, op ops, opts ...Option) (*Backend, error) {
	cfg := config{packMaxBytes: 64 << 20, packMaxEntries: 10000}
	for _, o := range opts {
		o(&cfg)
	}
	if err := fsbackend.ValidateBase(basePath); err != nil {
		return nil, err
	}
	if err := op.mkdirAllDo(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create pack base: %w", err)
	}
	packDir := filepath.Join(basePath, "packs")
	if err := op.mkdirAllDo(packDir, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create pack dir: %w", err)
	}
	loose, err := fsbackend.New(filepath.Join(basePath, "loose"))
	if err != nil {
		return nil, fmt.Errorf("cas: create loose backend: %w", err)
	}
	b := &Backend{
		base:         basePath,
		loose:        loose,
		packDir:      packDir,
		manifestPath: filepath.Join(packDir, "index.json"),
		cfg:          cfg,
		index:        make(map[string]packRecord),
		op:           op,
	}
	if err := b.loadIndex(); err != nil {
		return nil, err
	}
	if err := b.ensurePackFile(); err != nil {
		return nil, err
	}
	return b, nil
}

// BasePath returns the directory this backend's object bytes live under: the
// loose tree at <basePath>/loose, not the pack root passed to New. The pack
// files are an append-only mirror whose index is addressed separately, while the
// loose tree is what Get/Put/List/Stats/Clean operate on (Clean delegates to it),
// so a maintenance layer above the backend — cas/verify/sidecar stores its
// records there — belongs beside those bytes, where a crashed temp file is
// reclaimed by the same Clean a caller already runs.
func (b *Backend) BasePath() string { return b.loose.BasePath() }

func (b *Backend) loadIndex() error {
	data, err := b.op.readFileDo(b.manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cas: read pack manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("cas: decode pack manifest: %w", err)
	}
	filtered := make(map[string]packRecord, len(m.Entries))
	for key, rec := range m.Entries {
		// A key that is not a hex digest is a foreign entry, or one written by
		// a build that keyed the manifest by raw digest bytes (a form JSON
		// cannot round-trip). Either way it names no addressable object; the
		// loose tree still holds every object, so dropping it loses nothing.
		d, err := cas.ParseDigest(key)
		if err != nil {
			continue
		}
		valid, err := b.validPackRecord(rec)
		if err != nil || !valid {
			continue
		}
		filtered[string(d)] = rec
	}
	b.index = filtered
	return nil
}

func (b *Backend) pruneMissingPackEntriesLocked() {
	for key, rec := range b.index {
		valid, err := b.validPackRecord(rec)
		if err != nil || !valid {
			delete(b.index, key)
		}
	}
}

func (b *Backend) validPackRecord(rec packRecord) (bool, error) {
	if rec.Offset < 0 || rec.Size < 0 || rec.Offset > math.MaxInt64-rec.Size {
		return false, errInvalidPackRecord
	}
	rel, err := filepath.Rel(b.packDir, rec.Pack)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false, errInvalidPackRecord
	}
	info, err := os.Lstat(rec.Pack)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errInvalidPackRecord
	}
	if info.Size() < rec.Offset+rec.Size {
		return false, errInvalidPackRecord
	}
	return true, nil
}

// persistIndex writes the pack index atomically: the manifest carries every
// indexed digest in hex form (see manifest), so it survives the JSON round trip
// into the next process.
func (b *Backend) persistIndex() error {
	m := manifest{Entries: make(map[string]packRecord, len(b.index))}
	for key, rec := range b.index {
		m.Entries[cas.NewDigest([]byte(key)).String()] = rec
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("cas: encode pack manifest: %w", err)
	}
	tmp := b.manifestPath + ".tmp"
	if err := b.op.writeFileDo(tmp, data, 0o644); err != nil {
		return fmt.Errorf("cas: write pack manifest: %w", err)
	}
	return b.op.renameDo(tmp, b.manifestPath)
}

func (b *Backend) ensurePackFile() error {
	if b.packFile != nil {
		return nil
	}
	path := filepath.Join(b.packDir, "current.pack")
	f, err := b.op.openFileDo(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cas: open pack file: %w", err)
	}
	b.packFile = f
	b.packFilePath = path
	if fi, err := f.Stat(); err == nil {
		b.packBytes = fi.Size()
	}
	return nil
}

func (b *Backend) rotatePackFile() error {
	if b.packFile != nil {
		if err := b.packFile.Close(); err != nil {
			return err
		}
		b.packFile = nil
	}
	path := filepath.Join(b.packDir, fmt.Sprintf("pack-%d.pack", time.Now().UnixNano()))
	f, err := b.op.openFileDo(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cas: rotate pack file: %w", err)
	}
	b.packFile = f
	b.packFilePath = path
	b.packBytes = 0
	b.packEntries = 0
	return nil
}

// appendPackRecord appends one length-prefixed record for d to the active pack
// file, copying payloadSize bytes from r. The caller owns r and must position
// it at the payload start.
func (b *Backend) appendPackRecord(ctx context.Context, d cas.Digest, r io.Reader, payloadSize int64) error {
	if !b.cfg.enabled {
		return nil
	}
	if err := b.ensurePackFile(); err != nil {
		return err
	}
	if b.cfg.packMaxEntries > 0 && b.packEntries >= b.cfg.packMaxEntries {
		if err := b.rotatePackFile(); err != nil {
			return err
		}
	}
	if b.cfg.packMaxBytes > 0 && b.packBytes >= b.cfg.packMaxBytes {
		if err := b.rotatePackFile(); err != nil {
			return err
		}
	}
	if err := b.ensurePackFile(); err != nil {
		return err
	}
	payloadOffset, err := b.packFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("cas: seek pack file: %w", err)
	}
	header := make([]byte, 4+len(d)+8)
	binary.BigEndian.PutUint32(header[0:4], uint32(len(d)))
	copy(header[4:4+len(d)], d)
	binary.BigEndian.PutUint64(header[4+len(d):4+len(d)+8], uint64(payloadSize))
	if err := backend.WriteAll(ctx, b.packFile, header); err != nil {
		return fmt.Errorf("cas: write pack header: %w", err)
	}
	if _, err := io.Copy(b.packFile, backend.ContextReader{Ctx: ctx, R: io.LimitReader(r, payloadSize)}); err != nil {
		return fmt.Errorf("cas: write pack payload: %w", err)
	}
	b.packBytes += int64(4+len(d)+8) + payloadSize
	b.packEntries++
	b.index[string(d)] = packRecord{Pack: b.packFilePath, Offset: payloadOffset + int64(4+len(d)+8), Size: payloadSize}
	return b.persistIndex()
}

// Put stores an object under d. The write streams: with packing disabled the
// reader goes straight to the loose backend, and with packing enabled it is
// spooled to a scratch file first so the length-prefixed pack record can be
// written without ever holding the object in memory.
func (b *Backend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "pack: put"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.cfg.enabled {
		return b.op.putDo(ctx, b.loose, d, r)
	}

	spool, size, err := b.spoolObject(ctx, r)
	if err != nil {
		return err
	}
	defer func() {
		// Best effort: the spool file is scratch. Close before removing it,
		// because Windows cannot delete an open file.
		_ = spool.Close()
		_ = os.Remove(spool.Name())
	}()
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("cas: rewind spool file: %w", err)
	}
	if err := b.op.putDo(ctx, b.loose, d, spool); err != nil {
		return err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("cas: rewind spool file: %w", err)
	}
	return b.appendPackRecord(ctx, d, spool, size)
}

// spoolObject copies r to a scratch file in the pack directory and returns that
// file positioned at its end, plus the number of bytes copied.
func (b *Backend) spoolObject(ctx context.Context, r io.Reader) (*os.File, int64, error) {
	f, err := b.op.createTempDo(b.packDir, ".put-*.tmp")
	if err != nil {
		return nil, 0, fmt.Errorf("cas: create spool file: %w", err)
	}
	size, err := io.Copy(f, backend.ContextReader{Ctx: ctx, R: r})
	if err != nil {
		// Best effort: the spool file is scratch and the copy error is primary.
		// Close before removing — Windows cannot delete an open file.
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, 0, fmt.Errorf("cas: spool object: %w", err)
	}
	return f, size, nil
}

// Get returns bytes from the pack index when present; otherwise it falls back to the loose backend.
func (b *Backend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cas.CheckDigest(d, "pack: get"); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, ok := b.index[string(d)]; ok {
		valid, err := b.validPackRecord(rec)
		if err != nil {
			return nil, fmt.Errorf("cas: validate pack record: %w", err)
		}
		if !valid {
			delete(b.index, string(d))
			if persistErr := b.persistIndex(); persistErr != nil {
				return nil, persistErr
			}
		} else {
			f, err := b.op.openDo(rec.Pack)
			if err != nil {
				return nil, fmt.Errorf("cas: open pack file: %w", err)
			}
			return &sectionReadCloser{
				Reader: io.NewSectionReader(f, rec.Offset, rec.Size),
				closer: f,
			}, nil
		}
	}
	return b.op.getDo(ctx, b.loose, d)
}

// GetMany serves a batch of digests, opening each pack file at most once for
// the whole batch: the requested digests are grouped by the pack file that
// holds them, every group is opened a single time, and each object in it is
// served from that one open. A digest without a usable pack record falls back
// to the loose backend, one open each, exactly as Get does. GetMany satisfies
// cas.BatchGetter, so cas.GetMany dispatches to it, and it follows that
// function's contract — any order, one call per served object, the reader
// closed after fn returns, and the first error from a read, from fn or from a
// canceled ctx stops the batch.
//
// Each object's reader is a view onto its group's shared open: Close on the
// view releases nothing, because this method owns the file and closes it once
// the group has been served. fn must therefore consume what it needs before
// returning and must not retain the view.
//
// The pack index is consulted under the mutex, so the batch works from one
// consistent snapshot of the records, but the opens and the calls to fn run
// without it — a slow consumer never blocks Put or Delete.
func (b *Backend) GetMany(ctx context.Context, digests []cas.Digest, fn func(cas.Digest, io.ReadCloser) error) error {
	if fn == nil {
		return fmt.Errorf("cas: get many: nil fn")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	groups, loose, err := b.planGetMany(digests)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err := b.servePackGroup(ctx, group, fn); err != nil {
			return err
		}
	}
	for _, d := range loose {
		if err := ctx.Err(); err != nil {
			return err
		}
		reader, err := b.op.getDo(ctx, b.loose, d)
		if err != nil {
			return fmt.Errorf("cas: get many: %s: %w", d, err)
		}
		if err := callBatchFn(fn, d, reader); err != nil {
			return err
		}
	}
	return nil
}

// packGroup is one pack file's share of a GetMany batch: the file path plus the
// objects to serve from a single open of it, in requested order.
type packGroup struct {
	path    string
	records []packRequest
}

// packRequest locates one requested object inside its group's pack file.
type packRequest struct {
	digest cas.Digest
	offset int64
	size   int64
}

// planGetMany resolves the requested digests against the pack index in one
// consistent pass: objects with a live pack record are grouped by pack file,
// and the rest are returned as loose digests. A stale record is dropped and its
// object served from the loose backend, exactly as Get does, and the pruned
// index is persisted.
func (b *Backend) planGetMany(digests []cas.Digest) ([]packGroup, []cas.Digest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var groups []packGroup
	var loose []cas.Digest
	groupIndex := make(map[string]int)
	stale := false
	for _, d := range digests {
		if err := cas.CheckDigest(d, "pack: get many"); err != nil {
			return nil, nil, err
		}
		rec, ok := b.index[string(d)]
		if !ok {
			loose = append(loose, d)
			continue
		}
		valid, err := b.validPackRecord(rec)
		if err != nil {
			return nil, nil, fmt.Errorf("cas: validate pack record: %w", err)
		}
		if !valid {
			delete(b.index, string(d))
			stale = true
			loose = append(loose, d)
			continue
		}
		i, ok := groupIndex[rec.Pack]
		if !ok {
			i = len(groups)
			groupIndex[rec.Pack] = i
			groups = append(groups, packGroup{path: rec.Pack})
		}
		groups[i].records = append(groups[i].records, packRequest{digest: d, offset: rec.Offset, size: rec.Size})
	}
	if stale {
		if err := b.persistIndex(); err != nil {
			return nil, nil, err
		}
	}
	return groups, loose, nil
}

// servePackGroup opens one pack file, serves every record in the group from
// that single open, and closes the file when the group is done. A reader it
// hands to fn is a no-op closable view onto that shared open, so N objects in
// one pack cost exactly one open.
func (b *Backend) servePackGroup(ctx context.Context, group packGroup, fn func(cas.Digest, io.ReadCloser) error) (err error) {
	f, err := b.op.openDo(group.path)
	if err != nil {
		return fmt.Errorf("cas: open pack file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("cas: close pack file: %w", closeErr)
		}
	}()
	for _, req := range group.records {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		view := io.NopCloser(io.NewSectionReader(f, req.offset, req.size))
		if fnErr := fn(req.digest, view); fnErr != nil {
			return fnErr
		}
	}
	return nil
}

// callBatchFn calls fn with the reader and closes the reader exactly once,
// whether fn returned an error or panicked. fn's error wins; a failing Close is
// reported only when fn itself succeeded, so a close failure never masks the
// real cause.
func callBatchFn(fn func(cas.Digest, io.ReadCloser) error, d cas.Digest, reader io.ReadCloser) (err error) {
	defer func() {
		if closeErr := reader.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("cas: get many: close %s: %w", d, closeErr)
		}
	}()
	return fn(d, reader)
}

// Exists reports whether the object is present in either the pack index or the loose backend.
func (b *Backend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := cas.CheckDigest(d, "pack: exists"); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, ok := b.index[string(d)]; ok {
		valid, err := b.validPackRecord(rec)
		if err != nil {
			return false, fmt.Errorf("cas: validate pack record: %w", err)
		}
		if !valid {
			delete(b.index, string(d))
			if persistErr := b.persistIndex(); persistErr != nil {
				return false, persistErr
			}
		}
		if valid {
			return true, nil
		}
	}
	return b.op.existsDo(ctx, b.loose, d)
}

type sectionReadCloser struct {
	io.Reader
	closer io.Closer
}

// Close closes the packed object's backing file.
func (r *sectionReadCloser) Close() error {
	return r.closer.Close()
}

// Delete removes the digest from the loose backend and drops any index record.
func (b *Backend) Delete(ctx context.Context, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "pack: delete"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.op.deleteDo(ctx, b.loose, d); err != nil {
		return err
	}
	delete(b.index, string(d))
	if err := b.persistIndex(); err != nil {
		return err
	}
	return nil
}

// List returns all digests from the loose backend and the pack index.
func (b *Backend) List(ctx context.Context) ([]cas.Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	looseList, err := b.op.listDo(ctx, b.loose)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(looseList)+len(b.index))
	out := make([]cas.Digest, 0, len(looseList)+len(b.index))
	for _, d := range looseList {
		seen[string(d)] = struct{}{}
		out = append(out, d)
	}
	for key := range b.index {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cas.NewDigest([]byte(key)))
	}
	// Hex order equals byte order, so comparing raw digest bytes sorts exactly
	// like comparing the rendered hex strings — without allocating two strings
	// per comparison.
	slices.SortFunc(out, func(a, b cas.Digest) int { return bytes.Compare(a, b) })
	return out, nil
}

// Stats reports the loose backend totals plus the pack payload bytes in the index.
func (b *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneMissingPackEntriesLocked()
	stats, err := b.op.statsDo(ctx, b.loose)
	if err != nil {
		return nil, err
	}
	looseList, err := b.op.listDo(ctx, b.loose)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(looseList))
	for _, d := range looseList {
		seen[string(d)] = struct{}{}
	}
	for key, rec := range b.index {
		if rec.Size <= 0 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		stats.TotalSize += rec.Size
		stats.ObjectCount++
	}
	return stats, nil
}

// Size returns the stored object's size in bytes. Pack-backed objects report the
// pack payload size; loose-only objects fall back to the fs backend.
func (b *Backend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := cas.CheckDigest(d, "pack: size"); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, ok := b.index[string(d)]; ok {
		valid, err := b.validPackRecord(rec)
		if err != nil {
			return 0, fmt.Errorf("cas: validate pack record: %w", err)
		}
		if !valid {
			delete(b.index, string(d))
			if persistErr := b.persistIndex(); persistErr != nil {
				return 0, persistErr
			}
		} else {
			return rec.Size, nil
		}
	}
	return b.loose.Size(ctx, d)
}

// ModTime reports the physical modification time of the object. For a pack
// object this is the active pack file's timestamp; loose-only objects delegate
// to the fs backend.
func (b *Backend) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if err := cas.CheckDigest(d, "pack: mod time"); err != nil {
		return time.Time{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, ok := b.index[string(d)]; ok {
		valid, err := b.validPackRecord(rec)
		if err != nil {
			return time.Time{}, fmt.Errorf("cas: validate pack record: %w", err)
		}
		if !valid {
			delete(b.index, string(d))
			if persistErr := b.persistIndex(); persistErr != nil {
				return time.Time{}, persistErr
			}
		} else {
			fi, err := os.Stat(rec.Pack)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return time.Time{}, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
				}
				return time.Time{}, fmt.Errorf("cas: stat pack file: %w", err)
			}
			return fi.ModTime(), nil
		}
	}
	return b.loose.ModTime(ctx, d)
}

// Clean removes stale temporary files left by pack writes and loose fs writes.
// The loose tree is swept by its own backend; the pack directory is swept by
// fs.CleanTemp, which is the sweep the loose backend runs, so both trees agree
// on what counts as a leftover and one implementation serves both.
func (b *Backend) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	removed, err := b.loose.Clean(ctx, olderThan)
	if err != nil {
		return removed, err
	}
	packed, err := fsbackend.CleanTemp(ctx, b.packDir, olderThan)
	return removed + packed, err
}

// Close closes the active pack file.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.packFile == nil {
		return nil
	}
	err := b.packFile.Close()
	b.packFile = nil
	b.packFilePath = ""
	return err
}
