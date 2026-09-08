// Package backend defines the Backend interface and shared configuration for
// backend implementations (fs, memory, s3, …). Concrete backends live in
// subpackages and implement the interface defined in the parent cas package.
package backend

// Option configures a Backend. Functional options per library-design §4;
// never positional bool/int soup.
type Option func(*Config)

// Config holds backend configuration applied via Option. Fields are shared
// across backend implementations; a specific backend reads the ones it uses.
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
