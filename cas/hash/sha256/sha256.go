// Package sha256 is the hash algorithm shipped with go-cask: the client-side
// side of the core's Hasher seam (cas.Hasher).
//
// The cas core is deliberately algorithm-agnostic — a Digest is just bytes — so
// the algorithm a deployment uses is a property of its clients, not of the
// library. This package is the default those clients wire in: cmd/cask, the
// viewer, gitlike and the examples all construct a sha256.Hasher and hand it to
// cas.New, and use Parse/Format for the human-readable "sha256:hexdigest" form
// in URLs, CLI arguments and JSON responses.
//
// Nothing in cas imports this package.
package sha256

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	hashtype "hash"
	"io"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Name is the algorithm name used in the printable digest form.
const Name = "sha256"

// Size is the digest width in bytes.
const Size = sha256.Size

// Hasher implements cas.Hasher with sha256. The zero value is ready to use.
type Hasher struct{}

// New returns a sha256 Hasher for cas.New.
func New() Hasher { return Hasher{} }

// Digest implements cas.Hasher: it streams r through sha256 and returns the
// digest bytes.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/sha256: %w", err)
	}
	return cas.NewDigest(h.Sum(nil)), nil
}

// Validate implements cas.Hasher: a digest must be present and exactly Size
// bytes, otherwise it cannot name a stored object.
func (Hasher) Validate(d cas.Digest) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != Size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), Size)
	}
	return nil
}

// NewHasher returns a streaming sha256 hasher as the standard library
// hash.Hash, for callers that hash and spool at the same time (hash-on-write in
// the CLI and the example HTTP surface).
func NewHasher() hashtype.Hash { return sha256.New() }

// Of returns the digest of data.
func Of(data []byte) cas.Digest {
	sum := sha256.Sum256(data)
	return cas.NewDigest(sum[:])
}

// Format renders a digest in the printable "sha256:hexdigest" form, and the
// absent digest as "".
func Format(d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return Name + ":" + hex.EncodeToString(d)
}

// Parse accepts the printable "sha256:hexdigest" form and the bare hex form,
// and rejects anything else (including another algorithm's prefix) with
// cas.ErrInvalidDigest.
func Parse(s string) (cas.Digest, error) {
	body, ok := strings.CutPrefix(s, Name+":")
	if !ok {
		if strings.Contains(s, ":") {
			return nil, fmt.Errorf("%w: %q is not a %s digest", cas.ErrInvalidDigest, s, Name)
		}
		body = s
	}
	d, err := cas.ParseDigest(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", cas.ErrInvalidDigest, s)
	}
	if err := New().Validate(d); err != nil {
		return nil, fmt.Errorf("%w: %q", cas.ErrInvalidDigest, s)
	}
	return d, nil
}

// Short renders the first 8 hex characters of a digest for display, or
// "<absent>" for the absent digest.
func Short(d cas.Digest) string {
	if d.IsZero() {
		return "<absent>"
	}
	s := hex.EncodeToString(d)
	return s[:8]
}
