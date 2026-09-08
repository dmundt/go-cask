// Package backend defines the Backend interface and shared configuration for
// backend implementations (fs, memory, s3, …). Concrete backends live in
// subpackages and implement the interface defined in the parent cas package.
package backend

// Option configures a Backend. Functional options per library-design §4;
// never positional bool/int soup.
type Option func(*Config)

// Config holds backend configuration applied via Option. Config fields
// SHOULD NOT be shared across backend implementations — each backend
// subpackage defines its own Config type. The fields here are common to
// the filesystem backend; other backends (memory, s3, ...) may define
// their own Option types and Config structs with backend-specific fields.
type Config struct {
	FanOut    int
	FanLevels int
	DirSync   bool
}

// WithFanOut sets the number of hex characters per fan-out directory level.
// 0 means "flat" (no fan-out directories).
func WithFanOut(n int) Option {
	return func(c *Config) { c.FanOut = n }
}

// WithFanLevels sets the number of fan-out directory levels. 0 means "flat".
func WithFanLevels(n int) Option {
	return func(c *Config) { c.FanLevels = n }
}

// WithDirSync enables a best-effort fsync of the parent directory after the
// atomic rename that publishes an object, making the rename itself durable
// against crashes. Platforms that cannot sync directories (Windows) make it a
// no-op.
func WithDirSync() Option {
	return func(c *Config) { c.DirSync = true }
}
