// Package adler32 provides a maintenance-layer integrity checker for go-cask
// based on Adler-32.
package adler32

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/adler32"
	"io"

	"github.com/dmundt/go-cask/cas"
	hashutil "github.com/dmundt/go-cask/cas/hash"
)

// Name is the digest algorithm name used by this package.
const Name = "adler32"

// Size is the Adler-32 digest size in bytes.
const Size = 4

// Hasher implements cas.Hasher with Adler-32 for maintenance-only checks.
type Hasher struct{}

// New returns an Adler-32 CAS hasher.
func New() Hasher { return Hasher{} }

// Digest computes the Adler-32 digest of data read from r.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := adler32.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/verify/adler32: %w", err)
	}
	v := h.Sum32()
	b := make([]byte, Size)
	binary.BigEndian.PutUint32(b, v)
	return cas.NewDigest(b), nil
}

// Validate checks that d is a valid Adler-32 digest: present and exactly Size
// bytes.
func (Hasher) Validate(d cas.Digest) error {
	return hashutil.ValidateDigestSize(d, Name, Size)
}

// NewHasher returns a standard library Adler-32 hash.Hash32.
func NewHasher() hash.Hash32 { return adler32.New() }

// Of returns the Adler-32 digest of data.
func Of(data []byte) cas.Digest {
	v := adler32.Checksum(data)
	b := make([]byte, Size)
	binary.BigEndian.PutUint32(b, v)
	return cas.NewDigest(b)
}

// Format renders a digest in the printable "adler32:hexdigest" form, and the
// absent digest as "".
func Format(d cas.Digest) string { return hashutil.FormatDigest(Name, d) }

// Parse accepts the printable "adler32:hexdigest" form and the bare hex form,
// and rejects anything else with cas.ErrInvalidDigest.
func Parse(s string) (cas.Digest, error) { return hashutil.ParseDigest(Name, s, Size) }
