package repo

import (
	"fmt"

	"github.com/dmundt/go-cask/cas"
)

// UnknownTypeError reports that a digest's stored envelope names a type with
// no Decoder registered for it, or — from LookupStore — that no store is
// registered under a type name. Unwrap returns cas.ErrUnknownType, so a caller
// that only cares about the sentinel can use errors.Is(err, cas.ErrUnknownType)
// without depending on this concrete type; TypeName carries the exact
// versioned type name found in the envelope (or asked for), for a caller that
// wants to name the offending type (e.g. a verify pass enumerating
// unknown-type objects).
type UnknownTypeError struct {
	// Digest is the object's address. It is absent when the error reports a
	// lookup by type name (LookupStore) rather than a resolution.
	Digest cas.Digest
	// TypeName is the versioned type name read from the envelope.
	TypeName string
}

// Error implements error. A lookup by type name has no digest to name, so that
// message carries the type only.
func (e *UnknownTypeError) Error() string {
	if e.Digest.IsZero() {
		return fmt.Sprintf("cas/repo: unknown type %q", e.TypeName)
	}
	return fmt.Sprintf("cas/repo: %s: unknown type %q", e.Digest, e.TypeName)
}

// Unwrap returns cas.ErrUnknownType.
func (e *UnknownTypeError) Unwrap() error { return cas.ErrUnknownType }
