package repo

import (
	"fmt"

	"github.com/dmundt/go-cask/cas"
)

// UnknownTypeError reports that a digest's stored envelope names a type with
// no Decoder registered for it. Unwrap returns cas.ErrUnknownType, so a caller
// that only cares about the sentinel can use errors.Is(err, cas.ErrUnknownType)
// without depending on this concrete type; TypeName carries the exact
// versioned type name found in the envelope, for a caller that wants to name
// the offending type (e.g. a verify pass enumerating unknown-type objects).
type UnknownTypeError struct {
	// Digest is the object's address.
	Digest cas.Digest
	// TypeName is the versioned type name read from the envelope.
	TypeName string
}

// Error implements error.
func (e *UnknownTypeError) Error() string {
	return fmt.Sprintf("cas/repo: %s: unknown type %q", e.Digest, e.TypeName)
}

// Unwrap returns cas.ErrUnknownType.
func (e *UnknownTypeError) Unwrap() error { return cas.ErrUnknownType }
