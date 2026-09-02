package cas

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	hashtype "hash"
	"regexp"
	"strings"
	"sync"
)

// Hash is a content address: "algo:hexdigest" (e.g. "sha256:a1b2…"). Every
// reference between objects holds a full Hash — algorithm AND digest — never
// a bare digest, so one object graph may mix algorithms freely and a store
// can read any object whose algorithm is registered. Hashes are immutable
// value carriers. The zero value is a nil Hash.
type Hash interface {
	Algorithm() string // "sha1", "sha256", ...
	Bytes() []byte     // raw digest bytes
	String() string    // "algo:hexdigest"
	Equal(other Hash) bool
}

// hash is the concrete Hash implementation; equality is algorithm AND digest
// comparison.
type hash struct {
	algo  string
	bytes []byte
}

func (h hash) Algorithm() string { return h.algo }

func (h hash) Bytes() []byte {
	b := make([]byte, len(h.bytes))
	copy(b, h.bytes)
	return b
}

func (h hash) String() string { return h.algo + ":" + hex.EncodeToString(h.bytes) }

func (h hash) Equal(other Hash) bool {
	if other == nil {
		return false
	}
	return h.algo == other.Algorithm() && bytes.Equal(h.bytes, other.Bytes())
}

// HashBytes computes the content address of data with a registered
// algorithm: it streams through the built-in hasher when available, or uses
// a one-shot registered HashFunc otherwise. It returns ErrUnknownAlgorithm
// for an unregistered algorithm.
func HashBytes(algo string, data []byte) (Hash, error) {
	if newFn, ok := lookupStreamHash(algo); ok {
		h := newFn()
		h.Write(data)
		return NewHash(algo, h.Sum(nil))
	}
	if fn, ok := lookupHash(algo); ok {
		return fn(data), nil
	}
	return nil, fmt.Errorf("cas: %w: %q", ErrUnknownAlgorithm, algo)
}

// NewHasher returns a streaming hasher for a registered algorithm (the
// built-ins sha1/sha256). It returns ErrUnknownAlgorithm for algorithms
// registered only as one-shot HashFunc, which cannot stream — use HashBytes
// for those.
func NewHasher(algo string) (hashtype.Hash, error) {
	newFn, ok := lookupStreamHash(algo)
	if !ok {
		return nil, fmt.Errorf("cas: %w: %q does not support streaming", ErrUnknownAlgorithm, algo)
	}
	return newFn(), nil
}

// HashFunc computes the content address of data. Implementations MUST be
// deterministic and pure: identical input, identical Hash, no side effects.
type HashFunc func(data []byte) Hash

// NewHash builds a Hash from a registered algorithm name and raw digest
// bytes. It returns ErrUnknownAlgorithm if algo is not registered and
// ErrInvalidHash if digest is empty. RegisterHash must be called before
// NewHash for a custom algorithm.
func NewHash(algo string, digest []byte) (Hash, error) {
	if _, ok := lookupHash(algo); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algo)
	}
	if len(digest) == 0 {
		return nil, fmt.Errorf("%w: empty digest for %q", ErrInvalidHash, algo)
	}
	b := make([]byte, len(digest))
	copy(b, digest)
	return hash{algo: algo, bytes: b}, nil
}

// ParseHash reconstructs a Hash from its string form "algo:hexdigest". It
// rejects unknown algorithms (ErrUnknownAlgorithm) and malformed digests
// (ErrInvalidHash): empty algorithm or digest, non-lowercase or odd-length
// hex. The digest length is not validated against the algorithm — a custom
// registered algorithm may produce any digest width.
func ParseHash(s string) (Hash, error) {
	algo, hexPart, ok := strings.Cut(s, ":")
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	if !algoRe.MatchString(algo) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	if _, known := lookupHash(algo); !known {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, s)
	}
	if len(hexPart) == 0 || len(hexPart)%2 != 0 || !hexRe.MatchString(hexPart) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	digest, err := hex.DecodeString(hexPart)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHash, s)
	}
	return hash{algo: algo, bytes: digest}, nil
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
	RegisterHash("sha1", func(data []byte) Hash {
		sum := sha1.Sum(data)
		return hash{algo: "sha1", bytes: sum[:]}
	})
	RegisterHash("sha256", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		return hash{algo: "sha256", bytes: sum[:]}
	})
	registerStreamHash("sha1", sha1.New)
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

func lookupStreamHash(algo string) (func() hashtype.Hash, bool) {
	hashRegistryMu.RLock()
	defer hashRegistryMu.RUnlock()
	fn, ok := hashStreams[algo]
	return fn, ok
}

// RegisterHash registers a hash algorithm under name, making it usable by
// ParseHash, NewHash and NewStore. It replaces any previous function under
// the same name. Call it before constructing stores that use the algorithm.
func RegisterHash(algo string, fn HashFunc) {
	hashRegistryMu.Lock()
	defer hashRegistryMu.Unlock()
	hashRegistry[algo] = fn
}

func lookupHash(algo string) (HashFunc, bool) {
	hashRegistryMu.RLock()
	defer hashRegistryMu.RUnlock()
	fn, ok := hashRegistry[algo]
	return fn, ok
}
