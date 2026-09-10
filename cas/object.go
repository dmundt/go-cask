package cas

// Object[T] is a self-describing, reference-aware typed object: it knows
// its versioned type name and which digests it references. The generic core
// never interprets types or references — References() is the single source
// of truth for traversal, preloading, and GC reachability.
//
// Serialization is NOT an object concern: the Store[T] is configured with a
// Codec[T], and Store.Put writes the type + codec payload to the byte layer itself — the
// codec is the single serialization authority on write and read (cas-core
// §8 decision 1). Type() must return a versioned type name "<type>@<major>"
// (e.g. "commit@1") so several object-model majors can coexist in one store
// and the version travels with the bytes.
type Object[T any] interface {
	Type() string         // versioned type name, e.g. "blob@1"
	References() []Digest // digests this object points to; may be nil
}

// Validator is the optional object-invariant contract: a type stored in a
// Store[T] MAY additionally declare Validate() error, and the store then
// enforces it.
//
//   - Put/PutDedup call it before encoding, so an object that violates its own
//     invariants is never written.
//   - Get calls it after decoding, so an object that violates them — a
//     hand-crafted payload, or one written by another tool — is reported as
//     ErrCorrupt instead of being handed back in an impossible state.
//
// Invariants belong here rather than in a codec because they are properties of
// the object model, not of a wire format: the same rule must hold whichever
// Codec[T] a client chooses, so the store calls Validate whenever T declares it
// and the behavior is identical for JSON, gob, or any other codec.
//
// Validate MUST be deterministic and pure, and SHOULD be cheap: it runs on
// every Put and every Get (not on GetRaw, which does not decode).
type Validator interface {
	Validate() error
}
