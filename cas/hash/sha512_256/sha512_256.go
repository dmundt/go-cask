// Package sha512_256 is an alternative fast secure client-side hasher for
// go-cask using the standard library's SHA-512/256 digest.
//
// Policy: the cas core remains algorithm-agnostic, and callers choose the
// algorithm they want. SHA-256 remains the default recommended choice for new
// durable CAS data, while SHA-512/256 is a supported fast secure alternative.
// MD5 and SHA-1 are legacy or compatibility-only choices and should not be used
// for new content-addressed data.
//
// It follows the same seam as cas/hash/sha256: the cas core remains
// algorithm-agnostic and the caller injects Hasher implementations.
package sha512_256

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	hashtype "hash"
	"io"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Name is the algorithm name used in the printable digest form.
const Name = "sha512_256"

// Size is the digest width in bytes.
const Size = 32

// Hasher implements cas.Hasher with SHA-512/256. The zero value is ready to use.
type Hasher struct{}

// New returns a SHA-512/256 Hasher for cas.New.
func New() Hasher { return Hasher{} }

// Digest implements cas.Hasher: it streams r through SHA-512/256 and returns
// the digest bytes.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := sha512.New512_256()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/sha512_256: %w", err)
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

// NewHasher returns a streaming SHA-512/256 hasher as the standard library
// hash.Hash, for callers that hash and spool at the same time.
func NewHasher() hashtype.Hash { return sha512.New512_256() }

// Of returns the digest of data.
func Of(data []byte) cas.Digest {
	sum := sha512.Sum512_256(data)
	return cas.NewDigest(sum[:])
}

// Format renders a digest in the printable "sha512_256:hexdigest" form, and
// the absent digest as "".
func Format(d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return Name + ":" + hex.EncodeToString(d)
}

// Parse accepts the printable "sha512_256:hexdigest" form and the bare hex
// form, and rejects anything else (including another algorithm's prefix) with
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
