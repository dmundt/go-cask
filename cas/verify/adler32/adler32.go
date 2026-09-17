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

const Name = "adler32"

const Size = 4

// Hasher implements cas.Hasher with Adler-32 for maintenance-only checks.
type Hasher struct{}

func New() Hasher { return Hasher{} }

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

func (Hasher) Validate(d cas.Digest) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != Size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), Size)
	}
	return nil
}

func NewHasher() hash.Hash32 { return adler32.New() }

func Of(data []byte) cas.Digest {
	v := adler32.Checksum(data)
	b := make([]byte, Size)
	binary.BigEndian.PutUint32(b, v)
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
