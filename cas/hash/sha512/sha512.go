// Package sha512 is a standard-library-backed client-side hasher for go-cask.
//
// Policy: the cas core remains algorithm-agnostic. Callers choose the algorithm
// they want and inject a compatible cas.Hasher implementation when they build a
// Store. This package provides a full-width SHA-512 option alongside the
// repo's default recommendation, SHA-256.
package sha512

import (
	"crypto/sha512"
	"fmt"
	hashtype "hash"
	"io"

	"github.com/dmundt/go-cask/cas"
	hashutil "github.com/dmundt/go-cask/cas/hash"
)

// Name is the algorithm name used in the printable digest form.
const Name = "sha512"

// Size is the digest width in bytes.
const Size = sha512.Size

// Hasher implements cas.Hasher with SHA-512. The zero value is ready to use.
type Hasher struct{}

// New returns a SHA-512 Hasher for cas.New.
func New() Hasher { return Hasher{} }

// Digest implements cas.Hasher: it streams r through SHA-512 and returns the
// digest bytes.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := sha512.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/sha512: %w", err)
	}
	return cas.NewDigest(h.Sum(nil)), nil
}

// Validate implements cas.Hasher: a digest must be present and exactly Size
// bytes, otherwise it cannot name a stored object.
func (Hasher) Validate(d cas.Digest) error {
	return hashutil.ValidateDigestSize(d, Name, Size)
}

// NewHasher returns a streaming SHA-512 hasher as the standard library
// hash.Hash, for callers that hash and spool at the same time.
func NewHasher() hashtype.Hash { return sha512.New() }

// Of returns the digest of data.
func Of(data []byte) cas.Digest {
	sum := sha512.Sum512(data)
	return cas.NewDigest(sum[:])
}

// Format renders a digest in the printable "sha512:hexdigest" form, and the
// absent digest as "".
func Format(d cas.Digest) string {
	return hashutil.FormatDigest(Name, d)
}

// Parse accepts the printable "sha512:hexdigest" form and the bare hex form,
// and rejects anything else (including another algorithm's prefix) with
// cas.ErrInvalidDigest.
func Parse(s string) (cas.Digest, error) {
	return hashutil.ParseDigest(Name, s, Size)
}
