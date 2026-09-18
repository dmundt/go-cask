// Package adler32 provides a maintenance-layer integrity checker for go-cask
// based on Adler-32.
package adler32

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/adler32"
	"io"
	"strings"

	"github.com/dmundt/go-cask/cas"
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

// Validate checks that d is a valid Adler-32 digest.
func (Hasher) Validate(d cas.Digest) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != Size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), Size)
	}
	return nil
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

// Format formats d as an Adler-32 digest string.
func Format(d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return Name + ":" + hex.EncodeToString(d)
}

// Parse parses an Adler-32 digest string.
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
	if err := (Hasher{}).Validate(d); err != nil {
		return nil, fmt.Errorf("%w: %q", cas.ErrInvalidDigest, s)
	}
	return d, nil
}
