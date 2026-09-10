package cas

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"regexp"
)

// Digest is a content digest — the key an object is stored under.
//
// It is deliberately just bytes: cas names no hash algorithm, implements none,
// and cannot tell one digest width from another. The client owns the algorithm,
// hashes the bytes (Store does it through the injected Hasher) and decides what
// a digest means. This is the OCI/Docker split — the storage layer keys blobs by
// an opaque digest, and the code that knows the algorithm lives outside it —
// with one addition: the injected Hasher also validates a digest, so a key of
// the wrong width is still rejected at the store boundary.
//
// The zero value (nil) is the ABSENT digest: the one spelling of "no reference"
// in the library. IsZero reports it, String renders it as "", Equal treats it as
// equal to nothing, and object fields use it directly (an optional reference is
// tagged `omitzero`).
//
// A digest is immutable: NewDigest and Bytes copy, and the text form is
// lowercase hex (MarshalText/UnmarshalText, so encoding/json and every other
// codec that honors encoding.TextMarshaler store a reference as one hex string).
type Digest []byte

// NewDigest wraps raw digest bytes, copying them so the caller keeps ownership
// of b. A nil or empty slice produces the absent digest.
func NewDigest(b []byte) Digest {
	if len(b) == 0 {
		return nil
	}
	d := make(Digest, len(b))
	copy(d, b)
	return d
}

// IsZero reports whether the digest is absent (the zero value). Absent digests
// are valid as object fields and invalid as store keys.
func (d Digest) IsZero() bool { return len(d) == 0 }

// Equal reports whether both digests hold the same bytes. An absent digest
// equals nothing, including another absent one: two unknown references are not
// the same object.
func (d Digest) Equal(other Digest) bool {
	if d.IsZero() || other.IsZero() {
		return false
	}
	return bytes.Equal(d, other)
}

// Bytes returns a copy of the digest; nil for the absent digest. The copy keeps
// the digest immutable for the caller.
func (d Digest) Bytes() []byte {
	if len(d) == 0 {
		return nil
	}
	b := make([]byte, len(d))
	copy(b, d)
	return b
}

// String returns the lowercase-hex form, or "" for the absent digest. It is a
// rendering of the bytes only — no algorithm name is involved, since the core
// does not know one.
func (d Digest) String() string {
	if len(d) == 0 {
		return ""
	}
	return hex.EncodeToString(d)
}

// Prefix returns the first n characters of the lowercase-hex form for display —
// the viewer's 8-character short form is Prefix(8). n counts HEX CHARACTERS, not
// bytes. The method is total: it never panics and never returns an error, so a
// display helper can be used anywhere in a formatting call.
//
//   - the absent digest returns "" — the same spelling String uses — because
//     absence is a legitimate state (IsZero), not a failure;
//   - a digest whose hex form is shorter than n (a client hasher may produce
//     one: the core names no algorithm) is returned whole rather than sliced
//     out of range;
//   - n <= 0 returns "".
//
// A caller that wants a visible marker for absence ("<absent>") renders it
// itself: that is a presentation choice, not a property of the value.
func (d Digest) Prefix(n int) string {
	if n <= 0 {
		return ""
	}
	s := d.String()
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// MarshalText implements encoding.TextMarshaler: a digest is stored as its
// lowercase-hex string by encoding/json and any other codec that honors the
// interface. The absent digest renders as "".
func (d Digest) MarshalText() ([]byte, error) {
	if len(d) == 0 {
		return []byte{}, nil
	}
	return []byte(hex.EncodeToString(d)), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. An empty string means the
// absent digest; every other value must be lowercase hex. The parse is strict:
// a legacy "sha256:hexdigest" reference is rejected rather than reinterpreted,
// so an object written before the digest format changed fails loudly at decode
// instead of resolving to a different address.
func (d *Digest) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = nil
		return nil
	}
	if len(b)%2 != 0 || !hexRe.Match(b) {
		return fmt.Errorf("%w: %q is not a hex digest", ErrInvalidDigest, b)
	}
	raw := make([]byte, hex.DecodedLen(len(b)))
	if _, err := hex.Decode(raw, b); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidDigest, b)
	}
	*d = Digest(raw)
	return nil
}

// ParseDigest parses the canonical text form of a digest: lowercase hex. The
// core validates the shape only; whether the width matches the client's
// algorithm is the injected Hasher's job (Hasher.Validate).
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if err := d.UnmarshalText([]byte(s)); err != nil {
		return nil, err
	}
	if d.IsZero() {
		return nil, fmt.Errorf("%w: empty digest", ErrInvalidDigest)
	}
	return d, nil
}

// CheckDigest rejects an absent digest, so a caller handed a zero Digest (a
// forgotten parse, a zero-valued field) gets ErrInvalidDigest instead of an
// object written under a key that cannot mean anything. It is the guard the
// store and every backend apply to their key arguments.
func CheckDigest(d Digest, what string) error {
	if d.IsZero() {
		return fmt.Errorf("%w: %s: absent digest", ErrInvalidDigest, what)
	}
	return nil
}

// hexRe is the valid digest text shape: lowercase hex only.
var hexRe = regexp.MustCompile(`^[0-9a-f]+$`)
