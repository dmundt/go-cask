// Package backend provides shared types for Backend implementations.
// Concrete backends (fs, memory, s3, ...) define their own Config and
// option functions; the generic Option is a func that applies a backend's
// own configuration struct.
package backend

// Option configures a Backend. Each backend defines its own concrete Config
// type and functions that return Option to mutate it.
type Option func(any)