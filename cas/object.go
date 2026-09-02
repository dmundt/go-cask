package cas

// Object[T] is a self-describing, reference-aware typed object: it knows
// its versioned type name and which hashes it references. The generic core
// never interprets types or references — References() is the single source
// of truth for traversal, preloading, and GC reachability.
//
// Serialization is NOT an object concern: the Store[T] is configured with a
// Codec[T], and Store.Put builds the self-describing envelope
// {"type": "<type>@<major>", "data": "<base64 codec payload>"} itself — the
// codec is the single serialization authority on write and read (cas-core
// §8 decision 1). Type() must return a versioned type name "<type>@<major>"
// (e.g. "commit@1") so several object-model majors can coexist in one store
// and the version travels with the bytes.
type Object[T any] interface {
	Type() string       // versioned type name, e.g. "blob@1"
	References() []Hash // hashes this object points to; may be nil
}
