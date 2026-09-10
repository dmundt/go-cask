package cas

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	hashtype "hash"
	"regexp"
	"strings"
)

// SHA256 is the hash algorithm of the cas core. The core implements exactly one
// algorithm and has no registry (cas-core §4.2): the set of algorithms is fixed
// at compile time, so adding another one is a change to this package rather
// than a runtime registration. The name stays part of an address and of the
// backend layout, so the stored format remains self-describing and a store
// written by another build is still recognizable.
const SHA256 = "sha256"

// Hash is a content address: "sha256:hexdigest". Every reference between
// objects holds a full Hash — algorithm AND digest — never a bare digest, so an
// address is self-describing: a store written by a build with a different
// algorithm parses into ErrUnknownAlgorithm instead of being misread. Hashes
// are immutable value carriers: the fields are unexported, so the only ways to
// obtain one are ParseHash, NewHash and HashBytes, all of which validate.
//
// The zero value is the ABSENT hash — the one spelling of "no hash" in the
// library (cas-core §4.2). IsZero reports it, String renders it as "", and
// Equal treats it as equal to nothing. It is the natural zero for object
// fields, while an address handed to the byte layer must be present: Store and
// the backends reject a zero Hash with ErrInvalidHash rather than addressing an
// object it cannot mean.
//
// Hash deliberately carries no serialization, so the core stays free of any
// encoding. The JSON field shape — an object field that renders as
// "algo:hexdigest", omits an absent value and validates on decode — lives in
// the JSON codec as jsoncodec.Hash; another codec may define its own.
type Hash struct {
	algo  string
	bytes []byte
}

// Algorithm returns the algorithm name, always SHA256 for a present address and
// "" for the zero value.
func (h Hash) Algorithm() string { return h.algo }

// Bytes returns a copy of the raw digest; nil for the zero value. The copy
// keeps the address immutable for the caller.
func (h Hash) Bytes() []byte {
	if h.bytes == nil {
		return nil
	}
	b := make([]byte, len(h.bytes))
	copy(b, h.bytes)
	return b
}

// String returns the canonical "algo:hexdigest" form, or "" for the zero value.
func (h Hash) String() string {
	if h.algo == "" {
		return ""
	}
	return h.algo + ":" + hex.EncodeToString(h.bytes)
}

// IsZero reports whether the address is absent (the zero value). Absent
// addresses are valid as object fields and invalid as store keys.
func (h Hash) IsZero() bool { return h.algo == "" }

// Equal reports whether both addresses name the same algorithm and digest. An
// absent address equals nothing, including another absent one: two unknown
// hashes are not the same object.
func (h Hash) Equal(other Hash) bool {
	if h.IsZero() || other.IsZero() {
		return false
	}
	return h.algo == other.algo && bytes.Equal(h.bytes, other.bytes)
}

// HashBytes computes the content address of data: the sha256 digest of data,
// wrapped as a Hash. It cannot fail — the core's one algorithm is always
// available.
func HashBytes(data []byte) Hash {
	sum := sha256.Sum256(data)
	return Hash{algo: SHA256, bytes: sum[:]}
}

// NewHasher returns a streaming sha256 hasher as the standard library
// hash.Hash, so a caller can hash without buffering the whole object (Store.Put
// and the backend Verify paths stream through it).
func NewHasher() hashtype.Hash { return sha256.New() }

// NewHash builds a Hash from raw digest bytes. It returns ErrInvalidHash unless
// digest is exactly sha256.Size bytes: with one algorithm the digest width is
// fixed, so an address of any other width cannot name a stored object. The
// digest is copied, so the caller keeps ownership of its slice.
func NewHash(digest []byte) (Hash, error) {
	if len(digest) != sha256.Size {
		return Hash{}, fmt.Errorf("%w: digest is %d bytes, want %d", ErrInvalidHash, len(digest), sha256.Size)
	}
	b := make([]byte, len(digest))
	copy(b, digest)
	return Hash{algo: SHA256, bytes: b}, nil
}

// ParseHash reconstructs a Hash from its string form "sha256:hexdigest". It
// rejects malformed input with ErrInvalidHash (no colon, malformed algorithm
// name, wrong digest width, non-lowercase or non-hex digest) and an address
// naming an algorithm this build does not implement with ErrUnknownAlgorithm —
// the form a store written by another build parses into.
func ParseHash(s string) (Hash, error) {
	algo, hexPart, ok := strings.Cut(s, ":")
	if !ok || !algoRe.MatchString(algo) {
		return Hash{}, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	if algo != SHA256 {
		return Hash{}, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, s)
	}
	if len(hexPart) != hex.EncodedLen(sha256.Size) || !hexRe.MatchString(hexPart) {
		return Hash{}, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	digest, err := hex.DecodeString(hexPart)
	if err != nil {
		return Hash{}, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	return Hash{algo: algo, bytes: digest}, nil
}

// CheckHash rejects an absent address, so a caller handed a zero Hash (a
// forgotten parse, a zero-valued field) gets ErrInvalidHash instead of an
// object written under an address that cannot mean anything. It is the guard
// the store and every backend apply to their hash arguments.
func CheckHash(h Hash, what string) error {
	if h.IsZero() {
		return fmt.Errorf("%w: %s: absent hash", ErrInvalidHash, what)
	}
	return nil
}

var (
	// algoRe is the valid algorithm-name shape: lowercase alphanumerics. It
	// keeps a malformed name out of the "unknown algorithm" bucket, and out of
	// a backend path derived from it.
	algoRe = regexp.MustCompile(`^[a-z0-9]+$`)
	// hexRe is the valid digest shape: lowercase hex only.
	hexRe = regexp.MustCompile(`^[0-9a-f]+$`)
)
