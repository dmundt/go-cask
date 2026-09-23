// Package cas implements the core of a content-addressable store: binary
// objects are stored once under the digest of their content, as immutable,
// self-describing values that reference each other by digest. The package is
// layered — a non-generic byte layer (Digest, Backend, backends) below a
// generic typed layer (Object[T], Codec[T], Store[T], Walker[T], caches) —
// and knows nothing about application object models or hash algorithms; each
// app layers its own typed objects on top (the gitlike/ reference library
// demonstrates the pattern).
//
// The public surface and its contracts are specified in
// docs/specs/cas-core.md and docs/specs/library-design.md.
package cas

import "errors"

// Sentinel errors. Backends map their "not found" condition to ErrNotFound
// via %w; integrity checks return ErrDigestMismatch; parsing a digest returns
// ErrInvalidDigest; a stored type the reader does not handle — one with no
// registered resolver, or one outside a caller's fixed object model — returns
// ErrUnknownType; stored object data that cannot be parsed or decoded — a
// malformed or truncated envelope, a frame version this build cannot read, a
// payload the store codec rejects — returns ErrCorrupt; an object written with
// a different codec than the one reading it returns ErrCodecMismatch. Compare
// with errors.Is, never by string.
var (
	// ErrNotFound reports that a digest is absent from a backend.
	ErrNotFound = errors.New("cas: object not found")
	// ErrDigestMismatch reports content that does not match its digest.
	ErrDigestMismatch = errors.New("cas: digest mismatch")
	// ErrInvalidDigest reports malformed or unsupported digest bytes.
	ErrInvalidDigest = errors.New("cas: invalid digest")
	// ErrUnknownType reports a type dispatch miss: a stored type name with no
	// registered resolver, or one outside the fixed object model the caller
	// decodes. It answers a question about intact bytes and is never a parse
	// failure — a malformed envelope is ErrCorrupt, in every reader.
	ErrUnknownType = errors.New("cas: unknown object type or version")
	// ErrCorrupt reports invalid stored object data: an envelope that does not
	// parse (a truncated or oversized header field, an empty type name, a
	// frame version this build cannot read, a payload length that does not fit
	// the frame) or a payload the store's codec cannot decode, including one
	// that decodes to nil or violates its object's Validate.
	ErrCorrupt = errors.New("cas: corrupt object")
	// ErrCodecMismatch reports that an object was written with a different
	// codec than the one reading it. The stored bytes are intact — the reader
	// changed — so it is kept distinct from ErrCorrupt (damaged bytes) and
	// ErrUnknownType (a type the reader does not handle), and neither of those
	// is ever returned for a codec difference.
	ErrCodecMismatch = errors.New("cas: codec mismatch")
	// ErrUnsupported reports that a maintenance operation was requested that
	// the given backend cannot perform (e.g. age-based Sweep against a
	// backend that does not implement Statter). See Capabilities/CapabilitiesOf.
	ErrUnsupported = errors.New("cas: operation not supported by backend")
)
