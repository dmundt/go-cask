package cas

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestDigestAbsent pins the single spelling of "no reference": the zero value.
func TestDigestAbsent(t *testing.T) {
	var d Digest
	if !d.IsZero() {
		t.Fatal("the zero Digest must be absent")
	}
	if d.String() != "" || d.Bytes() != nil {
		t.Fatalf("zero Digest renders as %q / %v", d.String(), d.Bytes())
	}
	if d.Equal(d) {
		t.Fatal("an absent digest must not equal itself")
	}
	if err := CheckDigest(d, "test"); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("CheckDigest(zero) = %v, want ErrInvalidDigest", err)
	}
	present := NewDigest([]byte{0xab})
	if err := CheckDigest(present, "test"); err != nil {
		t.Fatalf("CheckDigest(present) = %v", err)
	}
}

// TestDigestBytesIsCopy pins immutability: NewDigest and Bytes copy.
func TestDigestBytesIsCopy(t *testing.T) {
	src := []byte{1, 2, 3}
	d := NewDigest(src)
	src[0] = 99
	if d.String() != "010203" {
		t.Fatalf("NewDigest aliased its input: %s", d)
	}
	got := d.Bytes()
	got[0] = 0xff
	if d.String() != "010203" {
		t.Fatalf("Bytes() aliased internal state: %s", d)
	}
}

// TestDigestEqual pins equality: same bytes are equal, and an absent digest
// equals nothing.
func TestDigestEqual(t *testing.T) {
	a, b := NewDigest([]byte{1, 2}), NewDigest([]byte{1, 2})
	c := NewDigest([]byte{1, 3})
	if !a.Equal(b) || !b.Equal(a) {
		t.Error("identical digests must be equal")
	}
	if a.Equal(c) || c.Equal(a) {
		t.Error("different digests must not be equal")
	}
	var absent Digest
	if a.Equal(absent) || absent.Equal(a) {
		t.Error("an absent digest must not compare equal")
	}
}

// TestDigestText pins the wire form of a reference: lowercase hex through
// encoding.TextMarshaler, so every codec that honors the interface stores one
// hex string and no per-type JSON code is needed.
func TestDigestText(t *testing.T) {
	d := NewDigest([]byte{0xde, 0xad, 0xbe, 0xef})
	raw, err := json.Marshal(struct {
		Ref Digest `json:"ref"`
	}{d})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ref":"deadbeef"}`; string(raw) != want {
		t.Fatalf("json.Marshal = %s, want %s", raw, want)
	}

	// Absent renders as "" and an `omitzero` field is dropped entirely.
	raw, err = json.Marshal(struct {
		Ref Digest `json:"ref,omitzero"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{}` {
		t.Fatalf("absent omitzero = %s, want {}", raw)
	}

	var back struct {
		Ref Digest `json:"ref"`
	}
	if err := json.Unmarshal([]byte(`{"ref":"deadbeef"}`), &back); err != nil {
		t.Fatal(err)
	}
	if !back.Ref.Equal(d) {
		t.Fatalf("round-trip = %s, want %s", back.Ref, d)
	}
}

// TestDigestRejectsLegacyText pins the deliberate break: a pre-change
// "sha256:hexdigest" reference is NOT reinterpreted — parsing fails, so an old
// stored object surfaces as a decode error (ErrCorrupt) instead of silently
// resolving to a different address.
func TestDigestRejectsLegacyText(t *testing.T) {
	legacy := "sha256:" + strings.Repeat("ab", 32)
	if _, err := ParseDigest(legacy); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("ParseDigest(legacy) = %v, want ErrInvalidDigest", err)
	}
	var d Digest
	if err := json.Unmarshal([]byte(`{"ref":"`+legacy+`"}`), &struct {
		Ref *Digest `json:"ref"`
	}{Ref: &d}); err == nil {
		t.Fatal("a legacy reference must fail to decode")
	}
}

// TestParseDigest pins the accepted shape (lowercase hex) and the rejections.
func TestParseDigest(t *testing.T) {
	ok := []string{"ab", "00", strings.Repeat("ab", 32)}
	for _, s := range ok {
		d, err := ParseDigest(s)
		if err != nil {
			t.Errorf("ParseDigest(%q) = %v", s, err)
			continue
		}
		if d.String() != s {
			t.Errorf("round-trip: %q -> %q", s, d.String())
		}
	}
	bad := []string{"", "AB", "a", "zz", "0xab", "ab cd", "sha256:ab"}
	for _, s := range bad {
		if _, err := ParseDigest(s); !errors.Is(err, ErrInvalidDigest) {
			t.Errorf("ParseDigest(%q) = %v, want ErrInvalidDigest", s, err)
		}
	}
}

// TestDigestUnmarshalAbsent pins that "" and null mean the absent digest.
func TestDigestUnmarshalAbsent(t *testing.T) {
	for _, in := range []string{`{"ref":""}`, `{"ref":null}`} {
		var v struct {
			Ref Digest `json:"ref"`
		}
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("Unmarshal(%s) = %v", in, err)
		}
		if !v.Ref.IsZero() {
			t.Fatalf("Unmarshal(%s) = %s, want absent", in, v.Ref)
		}
	}
}

// TestDigestMarshalAbsent pins the "" rendering for an always-present field.
func TestDigestMarshalAbsent(t *testing.T) {
	raw, err := json.Marshal(struct {
		Ref Digest `json:"ref"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ref":""}` {
		t.Fatalf("absent field = %s, want {\"ref\":\"\"}", raw)
	}
}

// TestDigestStringIsHexOnly pins that the core's rendering names no algorithm.
func TestDigestStringIsHexOnly(t *testing.T) {
	d := NewDigest(bytes.Repeat([]byte{0x0a}, 4))
	if d.String() != "0a0a0a0a" {
		t.Fatalf("String() = %q, want 0a0a0a0a", d.String())
	}
	if strings.Contains(d.String(), ":") {
		t.Fatalf("String() = %q must not carry an algorithm name", d.String())
	}
}

// FuzzParseDigest must never panic and must round-trip valid output.
func FuzzParseDigest(f *testing.F) {
	f.Add("ab")
	f.Add(strings.Repeat("ab", 32))
	f.Add("sha256:" + strings.Repeat("ab", 32))
	f.Add("")
	f.Add("zz")
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDigest(s)
		if err != nil {
			return
		}
		d2, err := ParseDigest(d.String())
		if err != nil {
			t.Fatalf("round-trip of %q failed: %v", d.String(), err)
		}
		if !d.Equal(d2) {
			t.Fatalf("round-trip changed digest: %s vs %s", d, d2)
		}
	})
}

// TestDigestPrefix pins the display helper's contract: n counts hex characters,
// the method is total (absent and n <= 0 are "", a short digest is returned
// whole), and the result is always a prefix of String().
func TestDigestPrefix(t *testing.T) {
	full := Digest(bytes.Repeat([]byte{0xab}, 32)) // 64 hex chars
	for _, tc := range []struct {
		name string
		d    Digest
		n    int
		want string
	}{
		{"absent", nil, 8, ""},
		{"absent with n <= 0", nil, 0, ""},
		{"n <= 0", full, 0, ""},
		{"negative n", full, -3, ""},
		{"viewer short form", full, 8, strings.Repeat("ab", 4)},
		{"single character", full, 1, "a"},
		{"whole digest", full, 64, strings.Repeat("ab", 32)},
		{"n beyond the hex form", full, 100, strings.Repeat("ab", 32)},
		{"shorter digest returned whole", Digest{0xab}, 8, "ab"},
		{"shorter digest, n == len", Digest{0xab, 0xcd}, 4, "abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.d.Prefix(tc.n)
			if got != tc.want {
				t.Fatalf("Prefix(%d) = %q, want %q", tc.n, got, tc.want)
			}
			if !strings.HasPrefix(tc.d.String(), got) {
				t.Fatalf("Prefix(%d) = %q is not a prefix of %q", tc.n, got, tc.d.String())
			}
		})
	}
}
