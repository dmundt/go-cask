// Package crc64 provides a CRC-64/ECMA-182 cas.Hasher for go-cask.
//
// Use it as the addressing hasher of a store that is deliberately keyed by
// CRC-64, or not at all: cas.Verify compares the recomputed digest to the
// object's address, so this hasher cannot validate an object addressed by
// another algorithm (cas/verify/README.md).
package crc64

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc64"
	"io"

	"github.com/dmundt/go-cask/cas"
	hashutil "github.com/dmundt/go-cask/cas/hash"
)

// Name is the digest algorithm name used by this package.
const Name = "crc64"

// Size is the CRC-64 digest size in bytes.
const Size = 8

// Hasher implements cas.Hasher with CRC-64/ECMA-182. It addresses and validates
// the objects of a store deliberately keyed by CRC-64; it cannot verify an object
// addressed by another algorithm.
type Hasher struct{}

// New returns a CRC-64/ECMA CAS hasher.
func New() Hasher { return Hasher{} }

// ecmaTable is the CRC-64/ECMA polynomial table, built once and shared by every
// use in this package. It is read-only after construction, so sharing it is
// safe and avoids rebuilding the 256-entry table on every digest.
var ecmaTable = crc64.MakeTable(crc64.ECMA)

// Digest computes the CRC-64/ECMA digest of data read from r.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	h := crc64.New(ecmaTable)
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/verify/crc64: %w", err)
	}
	v := h.Sum64()
	b := make([]byte, Size)
	binary.BigEndian.PutUint64(b, v)
	return cas.NewDigest(b), nil
}

// Validate checks that d is a valid CRC-64/ECMA digest: present and exactly
// Size bytes.
func (Hasher) Validate(d cas.Digest) error {
	return hashutil.ValidateDigestSize(d, Name, Size)
}

// NewHasher returns a standard library CRC-64 hash.Hash64.
func NewHasher() hash.Hash64 { return crc64.New(ecmaTable) }

// Of returns the CRC-64/ECMA digest of data.
func Of(data []byte) cas.Digest {
	v := crc64.Checksum(data, ecmaTable)
	b := make([]byte, Size)
	binary.BigEndian.PutUint64(b, v)
	return cas.NewDigest(b)
}

// Format renders a digest in the printable "crc64:hexdigest" form, and the
// absent digest as "".
func Format(d cas.Digest) string { return hashutil.FormatDigest(Name, d) }

// Parse accepts the printable "crc64:hexdigest" form and the bare hex form, and
// rejects anything else with cas.ErrInvalidDigest.
func Parse(s string) (cas.Digest, error) { return hashutil.ParseDigest(Name, s, Size) }
