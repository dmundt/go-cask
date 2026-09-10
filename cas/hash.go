package cas

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	hashtype "hash"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Hash is a content address: "algo:hexdigest" (e.g. "sha256:a1b2…"). Every
// reference between objects holds a full Hash — algorithm AND digest — never a
// bare digest, so one object graph may mix algorithms freely and a store can
// read any object whose algorithm is registered. Hashes are immutable value
// carriers: the fields are unexported, so the only ways to obtain one are
// ParseHash, NewHash and HashBytes, all of which validate.
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

// Algorithm returns the algorithm name ("sha256", or a registered custom
// algorithm); "" for the zero value.
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

// HashBytes computes the content address of data with a registered
// algorithm: it streams through the built-in hasher when available, or uses
// a one-shot registered HashFunc otherwise. It returns ErrUnknownAlgorithm
// for an unregistered algorithm.
func HashBytes(algo string, data []byte) (Hash, error) {
	if newFn, ok := LookupStreamHash(algo); ok {
		h := newFn()
		h.Write(data)
		return NewHash(algo, h.Sum(nil))
	}
	if fn, ok := LookupHash(algo); ok {
		return fn(data), nil
	}
	return Hash{}, fmt.Errorf("cas: %w: %q", ErrUnknownAlgorithm, algo)
}

// NewHasher returns a streaming hasher for a registered algorithm (the
// built-in sha256) as the standard library hash.Hash, so callers can
// stream bytes into it. It returns ErrUnknownAlgorithm for algorithms
// registered only as one-shot HashFunc, which cannot stream — use HashBytes
// for those.
func NewHasher(algo string) (hashtype.Hash, error) {
	newFn, ok := LookupStreamHash(algo)
	if !ok {
		return nil, fmt.Errorf("cas: %w: %q does not support streaming", ErrUnknownAlgorithm, algo)
	}
	return newFn(), nil
}

// HashFunc computes the content address of data. Implementations MUST be
// deterministic and pure: identical input, identical Hash, no side effects.
// Build the result with NewHash rather than a struct literal, so the address
// stays validated.
type HashFunc func(data []byte) Hash

// NewHash builds a Hash from a registered algorithm name and raw digest
// bytes. It returns ErrUnknownAlgorithm if algo is not registered and
// ErrInvalidHash if digest is empty. RegisterHash must be called before
// NewHash for a custom algorithm.
func NewHash(algo string, digest []byte) (Hash, error) {
	if _, ok := LookupHash(algo); !ok {
		return Hash{}, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algo)
	}
	if len(digest) == 0 {
		return Hash{}, fmt.Errorf("%w: empty digest for %q", ErrInvalidHash, algo)
	}
	b := make([]byte, len(digest))
	copy(b, digest)
	return Hash{algo: algo, bytes: b}, nil
}

// ParseHash reconstructs a Hash from its string form "algo:hexdigest". It
// rejects unknown algorithms (ErrUnknownAlgorithm) and malformed digests
// (ErrInvalidHash): empty algorithm or digest, non-lowercase or odd-length
// hex. The digest length is not validated against the algorithm — a custom
// registered algorithm may produce any digest width.
func ParseHash(s string) (Hash, error) {
	algo, hexPart, ok := strings.Cut(s, ":")
	if !ok {
		return Hash{}, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	if !algoRe.MatchString(algo) {
		return Hash{}, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	if _, known := LookupHash(algo); !known {
		return Hash{}, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, s)
	}
	if len(hexPart) == 0 || len(hexPart)%2 != 0 || !hexRe.MatchString(hexPart) {
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
	// algoRe is the valid algorithm-name shape: lowercase alphanumerics.
	algoRe = regexp.MustCompile(`^[a-z0-9]+$`)
	// hexRe is the valid digest shape: lowercase hex only.
	hexRe = regexp.MustCompile(`^[0-9a-f]+$`)
)

// registry of hash algorithms, populated at init with the built-ins. Reads
// are safe concurrently; runtime registration via RegisterHash is guarded by
// a mutex (registration is expected once at startup).
var (
	hashRegistry   = map[string]HashFunc{}
	hashStreams    = map[string]func() hashtype.Hash{}
	hashRegistryMu sync.RWMutex
)

func init() {
	RegisterHash("sha256", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		return Hash{algo: "sha256", bytes: sum[:]}
	})
	registerStreamHash("sha256", sha256.New)
}

// registerStreamHash registers an incremental (streaming) hasher for algo,
// used by Verify to check integrity without buffering the object. Custom
// algorithms registered only via RegisterHash fall back to buffering in
// Verify.
func registerStreamHash(algo string, newFn func() hashtype.Hash) {
	hashRegistryMu.Lock()
	defer hashRegistryMu.Unlock()
	hashStreams[algo] = newFn
}

// LookupStreamHash returns the registered streaming hash constructor for the
// given algorithm, or nil if none is registered.
func LookupStreamHash(algo string) (func() hashtype.Hash, bool) {
	hashRegistryMu.RLock()
	defer hashRegistryMu.RUnlock()
	fn, ok := hashStreams[algo]
	return fn, ok
}

// RegisterHash registers a hash algorithm under name, making it usable by
// ParseHash, NewHash and NewStore. It replaces any previous function under the
// same name and drops a previously registered streaming hasher for it, so the
// one-shot function is authoritative everywhere — HashBytes, Store and Verify
// keep agreeing on the address of the same algorithm name. Call it before
// constructing stores that use the algorithm.
//
// It panics on an invalid name (lowercase alphanumerics only) or a nil
// function: such a name can never be parsed back out of a hash string, and it
// would be unsafe as a store path element (fs backends derive directories from
// it).
func RegisterHash(algo string, fn HashFunc) {
	if !algoRe.MatchString(algo) {
		panic("cas: invalid algorithm name " + strconv.Quote(algo) + ` (must match ^[a-z0-9]+$)`)
	}
	if fn == nil {
		panic("cas: nil HashFunc for algorithm " + strconv.Quote(algo))
	}
	hashRegistryMu.Lock()
	defer hashRegistryMu.Unlock()
	hashRegistry[algo] = fn
	delete(hashStreams, algo)
}

// LookupHash returns the registered one-shot hash function for the given
// algorithm, or nil if none is registered.
func LookupHash(algo string) (HashFunc, bool) {
	hashRegistryMu.RLock()
	defer hashRegistryMu.RUnlock()
	fn, ok := hashRegistry[algo]
	return fn, ok
}
