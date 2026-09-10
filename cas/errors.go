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
// ErrInvalidDigest; an envelope with an unknown type name or major version
// returns ErrUnknownType; a stored payload that the store codec cannot decode
// returns ErrCorrupt. Compare with errors.Is, never by string.
var (
	ErrNotFound       = errors.New("cas: object not found")
	ErrDigestMismatch = errors.New("cas: digest mismatch")
	ErrInvalidDigest  = errors.New("cas: invalid digest")
	ErrUnknownType    = errors.New("cas: unknown object type or version")
	ErrCorrupt        = errors.New("cas: corrupt object")
)
