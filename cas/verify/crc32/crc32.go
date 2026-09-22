// Package crc32 provides a lightweight maintenance-layer integrity checker for
// go-cask.
//
// This package is intentionally not the default content-address algorithm. The
// core storage model stays boring and stable: object identity remains a cas.Digest
// produced by the caller's hasher, and the backend only stores raw bytes. This
// helper is for cheap, explicit consistency validation layered above that model.
package crc32

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"

	"github.com/dmundt/go-cask/cas"
	hashutil "github.com/dmundt/go-cask/cas/hash"
)

// Name is the checksum name used in the printable digest form.
const Name = "crc32"

// Size is the checksum width in bytes.
const Size = 4

// Hasher implements cas.Hasher with CRC32-IEEE for maintenance-only integrity
// checks. It is not a recommended content-address algorithm for durable object
// identity; it is a cheap validation layer, and callers should still choose their
// own stable addressing hash for object keys.
type Hasher struct{}

// New returns a CRC32 hasher for maintenance checks.
func New() Hasher { return Hasher{} }

// Digest implements cas.Hasher by streaming r through CRC32-IEEE and returning a
// 4-byte digest value.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/verify/crc32: %w", err)
	}
	v := h.Sum32()
	b := make([]byte, Size)
	binary.BigEndian.PutUint32(b, v)
	return cas.NewDigest(b), nil
}

// Validate implements cas.Hasher: a digest must be present and exactly Size
// bytes, otherwise it cannot name a valid CRC32 value.
func (Hasher) Validate(d cas.Digest) error {
	return hashutil.ValidateDigestSize(d, Name, Size)
}

// NewHasher returns the standard library hash.Hash32 implementation.
func NewHasher() hash.Hash32 { return crc32.NewIEEE() }

// Of returns the CRC32-IEEE checksum of data as a cas.Digest.
func Of(data []byte) cas.Digest {
	v := crc32.ChecksumIEEE(data)
	b := make([]byte, Size)
	binary.BigEndian.PutUint32(b, v)
	return cas.NewDigest(b)
}

// Format renders a digest in the printable "crc32:hexdigest" form, and the
// absent digest as "".
func Format(d cas.Digest) string { return hashutil.FormatDigest(Name, d) }

// Parse accepts the printable "crc32:hexdigest" form and the bare hex form,
// and rejects anything else with cas.ErrInvalidDigest.
func Parse(s string) (cas.Digest, error) { return hashutil.ParseDigest(Name, s, Size) }
