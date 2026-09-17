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

const Name = "crc64"

const Size = 8

// Hasher implements cas.Hasher with CRC-64/ECMA-182 for maintenance-only checks.
type Hasher struct{}

func New() Hasher { return Hasher{} }

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

func (Hasher) Validate(d cas.Digest) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != Size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), Size)
	}
	return nil
}

func NewHasher() hash.Hash64 { return crc64.New(crc64.MakeTable(crc64.ECMA)) }

func Of(data []byte) cas.Digest {
	tbl := crc64.MakeTable(crc64.ECMA)
	v := crc64.Checksum(data, tbl)
	b := make([]byte, Size)
	binary.BigEndian.PutUint64(b, v)
	return cas.NewDigest(b)
}

func Format(d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return Name + ":" + hex.EncodeToString(d)
}

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
