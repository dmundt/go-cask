// Package hash provides shared digest helpers for named hash algorithms.
//
// The helpers in this package focus on validation and formatting for concrete
// algorithm families and keep this logic separate from the core CAS digest type.
package hash

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// ValidateDigestSize enforces the canonical digest width for a named algorithm.
func ValidateDigestSize(d cas.Digest, name string, size int) error {
	if d.IsZero() {
		return fmt.Errorf("%w: absent digest", cas.ErrInvalidDigest)
	}
	if len(d) != size {
		return fmt.Errorf("%w: digest is %d bytes, want %d", cas.ErrInvalidDigest, len(d), size)
	}
	return nil
}

// FormatDigest returns the printable algorithm:hex form for a digest.
func FormatDigest(name string, d cas.Digest) string {
	if d.IsZero() {
		return ""
	}
	return name + ":" + hex.EncodeToString(d)
}

// ParseDigest accepts either the algorithm-prefixed form or bare hex and then
// validates the digest against the expected width. The returned error keeps the
// underlying cause on the chain — both cas.ParseDigest and ValidateDigestSize
// already wrap cas.ErrInvalidDigest — so a caller can still match the sentinel
// with errors.Is while keeping the detail that explains the rejection.
func ParseDigest(name, s string, size int) (cas.Digest, error) {
	body, ok := strings.CutPrefix(s, name+":")
	if !ok {
		if strings.Contains(s, ":") {
			return nil, fmt.Errorf("%w: %q is not a %s digest", cas.ErrInvalidDigest, s, name)
		}
		body = s
	}
	d, err := cas.ParseDigest(body)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	if err := ValidateDigestSize(d, name, size); err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	return d, nil
}
