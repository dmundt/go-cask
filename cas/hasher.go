package cas

import "io"

// Hasher is the client's hash algorithm, supplied to a Store. The core names no
// algorithm and implements none: it asks the Hasher for the digest of the bytes
// it is about to store, and asks it to validate a digest a caller passed in.
//
// A client typically uses the shipped sha256 implementation
// (cas/hash/sha256) and may substitute any algorithm — blake3, sha512, a
// truncated digest, even a non-cryptographic key — without changing the core.
//
// Implementations MUST be deterministic (identical bytes, identical Digest) and
// pure, and MUST be safe for concurrent use: one Hasher instance serves every
// Store operation.
type Hasher interface {
	// Digest returns the digest of everything readable from r. It streams: a
	// large object is never buffered by the caller or the core.
	Digest(r io.Reader) (Digest, error)

	// Validate reports whether d is a well-formed digest for this algorithm —
	// its width, typically. The store applies it to every digest a caller
	// supplies (Get/GetRaw/Exists/Delete) and to the result of Digest, so a key
	// that cannot name an object is rejected with ErrInvalidDigest rather than
	// silently missing.
	Validate(d Digest) error
}
