// Package cas implements the core of a content-addressable store: binary
// objects are stored once under the hash of their content, as immutable,
// self-describing values that reference each other by hash. The package is
// layered — a non-generic byte layer (Hash, RawStore, backends) below a
// generic typed layer (Object[T], Codec[T], Store[T], Walker[T], caches) —
// and knows nothing about application object models; each app layers its own
// typed objects on top (the gitlike example in examples/gitlike demonstrates
// the pattern).
//
// The public surface and its contracts are specified in
// docs/instructions/cas-core.md and
// docs/instructions/library-design.md.
package cas

import "errors"

// Sentinel errors. Backends map their "not found" condition to ErrNotFound
// via %w; integrity checks return ErrHashMismatch; hash parsing returns
// ErrInvalidHash / ErrUnknownAlgorithm; an envelope with an unknown type
// name or major version returns ErrUnknownType; a stored payload that the
// store codec cannot decode returns ErrCorrupt. Compare with errors.Is,
// never by string.
var (
	ErrNotFound         = errors.New("cas: object not found")
	ErrHashMismatch     = errors.New("cas: hash mismatch")
	ErrUnknownAlgorithm = errors.New("cas: unknown hash algorithm")
	ErrInvalidHash      = errors.New("cas: invalid hash")
	ErrUnknownType      = errors.New("cas: unknown object type or version")
	ErrCorrupt          = errors.New("cas: corrupt object")
)
