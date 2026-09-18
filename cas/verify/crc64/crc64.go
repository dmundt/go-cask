// Package crc64 provides a maintenance-layer integrity checker for go-cask based
// on CRC-64/ECMA-182.
package crc64

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc64"
	"io"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Name is the digest algorithm name used by this package.
const Name = "crc64"

// Size is the CRC-64 digest size in bytes.
const Size = 8

// Hasher implements cas.Hasher with CRC-64/ECMA-182 for maintenance-only checks.
type Hasher struct{}

// New returns a CRC-64/ECMA CAS hasher.
func New() Hasher { return Hasher{} }

// Digest computes the CRC-64/ECMA digest of data read from r.
func (Hasher) Digest(r io.Reader) (cas.Digest, error) {
	tbl := crc64.MakeTable(crc64.ECMA)
	h := crc64.New(tbl)
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("cas/verify/crc64: %w", err)
	}
	v := h.Sum64()
	b := make([]byte, Size)
	binary.BigEndian.PutUint64(b, v)
	return cas.NewDigest(b), nil
}

// Validate checks that d is a valid CRC-64/ECMA digest.
func (Hasher) Validate(d cas.Digest) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != Size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), Size)
	}
	return nil
}

// NewHasher returns a standard library CRC-64 hash.Hash64.
func NewHasher() hash.Hash64 { return crc64.New(crc64.MakeTable(crc64.ECMA)) }

// Of returns the CRC-64/ECMA digest of data.
func Of(data []byte) cas.Digest {
	tbl := crc64.MakeTable(crc64.ECMA)
	v := crc64.Checksum(data, tbl)
	b := make([]byte, Size)
	binary.BigEndian.PutUint64(b, v)
	return cas.NewDigest(b)
}

// Format formats d as a CRC-64/ECMA digest string.
func Format(d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return Name + ":" + hex.EncodeToString(d)
}

// Parse parses a CRC-64/ECMA digest string.
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
