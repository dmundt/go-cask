package sidecar

import (
	"fmt"
	"path/filepath"

	"github.com/dmundt/go-cask/cas"
)

// DefaultMaxRecordBytes bounds a record read. A v1 record is a few hundred
// bytes; the cap turns a damaged or hostile file in .meta into cas.ErrCorrupt
// instead of an unbounded allocation.
const DefaultMaxRecordBytes = 4096

// basePathReporter is the optional interface a backend implements when its
// objects live under a path the caller can name. fs.Backend and packfs.Backend
// implement it; a backend without durable bytes of its own (backmem.Backend) does
// not, and gets its base from WithBase.
type basePathReporter interface {
	BasePath() string
}

// Option configures a Backend. Options are applied in order by New.
type Option func(*config)

// config is the resolved New configuration.
type config struct {
	base           string
	algo           string
	hasher         cas.Hasher
	dirSync        bool
	maxRecordBytes int64
}

// WithBase sets the directory the caller's objects live under — the same path
// passed to the backend's constructor, because the record directory is a
// subdirectory of it. It is required for a backend that does not report its own
// base path, and it is checked against that report when it does.
func WithBase(path string) Option {
	return func(c *config) { c.base = path }
}

// WithChecksum sets the checksum the writer records and the algorithm name it
// records it under. The name is what a reader compares (for example
// crc32.Name): crc32 and adler32 are both four bytes wide, so the width alone
// cannot tell a wrong-algorithm read from corruption. There is no registry —
// the client owns the algorithm, and this is where it names it.
//
// It is required to record (Put) and optional otherwise: a reader that only
// verifies or reconciles names its algorithm on Verifier, and a caller that
// does neither — `cask gc` reconciling records — needs none.
func WithChecksum(algo string, hasher cas.Hasher) Option {
	return func(c *config) {
		c.algo = algo
		c.hasher = hasher
	}
}

// WithDirSync fsyncs the record directory after a record rename, so a record
// survives a power loss that the rename itself does not. It is off by default,
// like the filesystem backend's directory sync (defaults §4), and it is a no-op
// on Windows, where a directory cannot be opened for fsync.
func WithDirSync() Option {
	return func(c *config) { c.dirSync = true }
}

// WithMaxRecordBytes caps how many bytes a record read accepts. A record larger
// than the cap is cas.ErrCorrupt, never a silently skipped file. Zero or a
// negative value restores DefaultMaxRecordBytes.
func WithMaxRecordBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxRecordBytes = n
		}
	}
}

// resolveBase decides which base the records live under and refuses a
// disagreement: a decorator whose records sit beside a different store's bytes
// would report one store's objects against another's checksums.
func resolveBase(inner cas.Backend, c *config) (string, error) {
	reporter, ok := inner.(basePathReporter)
	if !ok {
		if c.base == "" {
			return "", fmt.Errorf("sidecar: WithBase is required for a backend that does not report a base path")
		}
		return filepath.Clean(c.base), nil
	}
	reported := filepath.Clean(reporter.BasePath())
	if c.base == "" {
		return reported, nil
	}
	if filepath.Clean(c.base) != reported {
		return "", fmt.Errorf("sidecar: base %q does not match the backend's base %q", filepath.Clean(c.base), reported)
	}
	return reported, nil
}
