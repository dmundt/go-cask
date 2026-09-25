package hash

import (
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestValidateDigestSize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		d       cas.Digest
		size    int
		wantErr bool
	}{
		{name: "valid", d: cas.NewDigest([]byte{1, 2, 3, 4}), size: 4, wantErr: false},
		{name: "zero", d: cas.Digest{}, size: 4, wantErr: true},
		{name: "short", d: cas.NewDigest([]byte{1, 2}), size: 4, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDigestSize(tc.d, "test", tc.size)
			if tc.wantErr && err == nil {
				t.Fatal("ValidateDigestSize returned nil error, want non-nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateDigestSize returned unexpected error: %v", err)
			}
		})
	}
}

func TestFormatDigestAndParseDigest(t *testing.T) {
	d := cas.NewDigest([]byte{0x0a, 0x0b, 0x0c})
	if got := FormatDigest("sha256", d); got != "sha256:0a0b0c" {
		t.Fatalf("FormatDigest = %q, want %q", got, "sha256:0a0b0c")
	}
	if got := FormatDigest("sha256", cas.Digest{}); got != "" {
		t.Fatalf("FormatDigest(absent) = %q, want empty", got)
	}

	parsed, err := ParseDigest("sha256", "sha256:0a0b0c", 3)
	if err != nil {
		t.Fatalf("ParseDigest returned unexpected error: %v", err)
	}
	if !parsed.Equal(d) {
		t.Fatalf("ParseDigest returned %q, want %q", parsed, d)
	}

	parsedBare, err := ParseDigest("sha256", "0a0b0c", 3)
	if err != nil {
		t.Fatalf("ParseDigest bare hex returned unexpected error: %v", err)
	}
	if !parsedBare.Equal(d) {
		t.Fatalf("ParseDigest bare hex returned %q, want %q", parsedBare, d)
	}

	if _, err := ParseDigest("sha256", "md5:0a0b0c", 3); err == nil {
		t.Fatal("ParseDigest should reject mismatched prefixes")
	}
	if _, err := ParseDigest("sha256", "sha256:zzzzzz", 3); err == nil {
		t.Fatal("ParseDigest should reject invalid hex")
	}
	if _, err := ParseDigest("sha256", "sha256:0a0b0", 3); err == nil {
		t.Fatal("ParseDigest should reject mismatched digest width")
	}
}

// TestParseDigestReportsAWellFormedDigestOfTheWrongWidth pins the width branch:
// hex that parses but is not the algorithm's canonical size is rejected by
// ValidateDigestSize and the caller keeps both the offending text and the
// underlying cause on the error chain (cas.ErrInvalidDigest).
func TestParseDigestReportsAWellFormedDigestOfTheWrongWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"bare hex one byte short", "0a0b0c"},
		{"prefixed hex one byte short", "sha256:0a0b0c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDigest("sha256", tc.text, 4)
			if err == nil {
				t.Fatalf("ParseDigest(%q, size 4) = %s, want an error", tc.text, d)
			}
			if !errors.Is(err, cas.ErrInvalidDigest) {
				t.Fatalf("ParseDigest(%q, size 4) = %v, want cas.ErrInvalidDigest", tc.text, err)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("ParseDigest(%q, size 4) = %v, want it to quote the input", tc.text, err)
			}
			if d != nil {
				t.Fatalf("ParseDigest(%q, size 4) = %s, want no digest", tc.text, d)
			}
		})
	}
}
