// Package crc32 provides a CRC-32/IEEE cas.Hasher for go-cask.
//
// It is a checksum, not a strong content address: it is the addressing hasher of
// a store that is deliberately keyed by CRC-32 (fixtures, benchmarks, data
// imported from systems that key by checksum), and it verifies those same
// objects. It cannot validate an object addressed by another algorithm, because
// cas.Verify recomputes the digest with the hasher it is given and compares it to
// the object's address: Validate rejects a digest of another width with
// cas.ErrInvalidDigest before any bytes are read. A cheap check above a
// strongly-addressed store would need a per-object checksum stored beside the
// object, which go-cask does not implement (cas/verify/README.md, extensions §3.1).
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

// Hasher implements cas.Hasher with CRC-32/IEEE. It addresses and validates the
// objects of a store that is deliberately keyed by CRC-32; it cannot verify an
// object addressed by another algorithm, because Verify compares the recomputed
// checksum to the object's address.
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
