package pack

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
)

// Config is the opt-in pack backend configuration.
type config struct {
	enabled       bool
	packMaxBytes  int64
	packMaxEntries int
}

// WithEnabled turns packfile mode on. Packfiles are intentionally opt-in.
func WithEnabled() backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.enabled = true
		}
	}
}

// WithPackMaxBytes rotates the active pack when the append-only file reaches the
// configured size. 0 means unlimited.
func WithPackMaxBytes(maxBytes int64) backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.packMaxBytes = maxBytes
		}
	}
}

// WithPackMaxEntries rotates the active pack once the in-memory index grows past
// the configured count. 0 means unlimited.
func WithPackMaxEntries(maxEntries int) backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.packMaxEntries = maxEntries
		}
	}
}

type packRecord struct {
	Pack   string `json:"pack"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
}

type manifest struct {
	Entries map[string]packRecord `json:"entries"`
}

// Backend is a filesystem-backed pack extension. It stores objects in a loose
// backend and optionally mirrors them into an append-only pack file plus a small
// JSON index that points to the payload offset within the pack.
type Backend struct {
	mu             sync.Mutex
	base           string
	loose          *fsbackend.Backend
	manifestPath   string
	packDir        string
	packFilePath   string
	packFile       *os.File
	packBytes      int64
	packEntries    int
	cfg            config
	index          map[string]packRecord
}

var _ cas.Backend = (*Backend)(nil)

// New creates a pack-backed filesystem backend. It stays opt-in: without
// WithEnabled, the backend behaves like the regular loose fs backend.
func New(basePath string, opts ...backend.Option) (*Backend, error) {
	cfg := config{packMaxBytes: 64 << 20, packMaxEntries: 10000}
	for _, o := range opts {
		o(&cfg)
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create pack base: %w", err)
	}
	packDir := filepath.Join(basePath, "packs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
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
	}
	if err := b.loadIndex(); err != nil {
		return nil, err
	}
	if err := b.ensurePackFile(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Backend) loadIndex() error {
	data, err := os.ReadFile(b.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cas: read pack manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("cas: decode pack manifest: %w", err)
	}
	if m.Entries == nil {
		m.Entries = make(map[string]packRecord)
	}
	b.index = m.Entries
	return nil
}

func (b *Backend) persistIndex() error {
	m := manifest{Entries: b.index}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("cas: encode pack manifest: %w", err)
	}
	tmp := b.manifestPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("cas: write pack manifest: %w", err)
	}
	return os.Rename(tmp, b.manifestPath)
}

func (b *Backend) ensurePackFile() error {
	if b.packFile != nil {
		return nil
	}
	path := filepath.Join(b.packDir, "current.pack")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cas: rotate pack file: %w", err)
	}
	b.packFile = f
	b.packFilePath = path
	b.packBytes = 0
	b.packEntries = 0
	return nil
}

func (b *Backend) appendPackRecord(d cas.Digest, data []byte) error {
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
	payloadSize := int64(len(data))
	payloadOffset, err := b.packFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("cas: seek pack file: %w", err)
	}
	var header [4 + 32 + 8]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(d)))
	copy(header[4:4+len(d)], d)
	binary.BigEndian.PutUint64(header[4+len(d):4+len(d)+8], uint64(payloadSize))
	if _, err := b.packFile.Write(header[:4+len(d)+8]); err != nil {
		return fmt.Errorf("cas: write pack header: %w", err)
	}
	if _, err := b.packFile.Write(data); err != nil {
		return fmt.Errorf("cas: write pack payload: %w", err)
	}
	b.packBytes += int64(4+len(d)+8) + payloadSize
	b.packEntries++
	b.index[string(d)] = packRecord{Pack: b.packFilePath, Offset: payloadOffset + int64(4+len(d)+8), Size: payloadSize}
	return b.persistIndex()
}

// Put stores an object in the loose backend and mirrors it into a pack file when
// the pack layer is enabled.
func (b *Backend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "pack: put"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	data, err := io.ReadAll(backend.ContextReader{Ctx: ctx, R: r})
	if err != nil {
		return fmt.Errorf("cas: read object: %w", err)
	}
	if err := b.loose.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return err
	}
	if !b.cfg.enabled {
		return nil
	}
	return b.appendPackRecord(d, data)
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
		f, err := os.Open(rec.Pack)
		if err != nil {
			return nil, fmt.Errorf("cas: open pack file: %w", err)
		}
		data := make([]byte, rec.Size)
		if _, err := io.NewSectionReader(f, rec.Offset, rec.Size).Read(data); err != nil && err != io.EOF {
			_ = f.Close()
			return nil, fmt.Errorf("cas: read pack entry: %w", err)
		}
		_ = f.Close()
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return b.loose.Get(ctx, d)
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
	if _, ok := b.index[string(d)]; ok {
		return true, nil
	}
	return b.loose.Exists(ctx, d)
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
	if err := b.loose.Delete(ctx, d); err != nil {
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
	looseList, err := b.loose.List(ctx)
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
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// Stats reports the loose backend totals plus the pack payload bytes in the index.
func (b *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stats, err := b.loose.Stats(ctx)
	if err != nil {
		return nil, err
	}
	looseList, err := b.loose.List(ctx)
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
