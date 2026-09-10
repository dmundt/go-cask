package sha256_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256hash "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestShortClampsShortDigest pins that a digest shorter than the 8-character
// display width is rendered whole instead of panicking: Short is a display
// helper and may be handed a digest this package's hasher did not produce.
func TestShortClampsShortDigest(t *testing.T) {
	if got := sha256hash.Short(cas.Digest{0xab}); got != "ab" {
		t.Errorf("Short(1 byte) = %q, want %q", got, "ab")
	}
	if got := sha256hash.Short(nil); got != "<absent>" {
		t.Errorf("Short(absent) = %q, want %q", got, "<absent>")
	}
	if got := sha256hash.Short(sha256hash.Of([]byte("x"))); len(got) != 8 {
		t.Errorf("Short(32 bytes) = %q, want 8 hex characters", got)
	}
}

// TestOfMatchesStdlib pins the digest bytes against crypto/sha256.
func TestOfMatchesStdlib(t *testing.T) {
	data := []byte("hash me")
	sum := sha256.Sum256(data)
	got := sha256hash.Of(data)
	if hex.EncodeToString(got) != hex.EncodeToString(sum[:]) {
		t.Fatalf("Of = %s, want %s", got, hex.EncodeToString(sum[:]))
	}
	if len(got) != sha256hash.Size {
		t.Fatalf("Of returned %d bytes, want %d", len(got), sha256hash.Size)
	}
}

// TestHasherDigestStreams pins the Hasher seam: Digest reads a whole stream and
// agrees with Of.
func TestHasherDigestStreams(t *testing.T) {
	data := []byte(strings.Repeat("stream", 100))
	got, err := sha256hash.New().Digest(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(sha256hash.Of(data)) {
		t.Fatalf("Digest = %s, want %s", got, sha256hash.Of(data))
	}
}

// TestHasherValidate pins the width check that keeps a key of the wrong size
// out of a store.
func TestHasherValidate(t *testing.T) {
	h := sha256hash.New()
	if err := h.Validate(sha256hash.Of([]byte("x"))); err != nil {
		t.Fatalf("Validate(present) = %v", err)
	}
	for _, d := range []cas.Digest{nil, cas.NewDigest([]byte{1, 2, 3}), cas.NewDigest(make([]byte, 64))} {
		if err := h.Validate(d); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Validate(%d bytes) = %v, want ErrInvalidDigest", len(d), err)
		}
	}
}

// TestNewHasherMatchesOf pins the streaming hash.Hash path used by the CLI and
// the example HTTP surface against the one-shot helper.
func TestNewHasherMatchesOf(t *testing.T) {
	data := []byte("stream and spool")
	hh := sha256hash.NewHasher()
	hh.Write(data)
	if got := hex.EncodeToString(hh.Sum(nil)); got != hex.EncodeToString(sha256hash.Of(data)) {
		t.Fatalf("NewHasher sum = %s", got)
	}
}

// TestFormatParse pins the printable form the clients use in URLs, CLI
// arguments and JSON responses.
func TestFormatParse(t *testing.T) {
	d := sha256hash.Of([]byte("printable"))
	printable := sha256hash.Format(d)
	if !strings.HasPrefix(printable, sha256hash.Name+":") {
		t.Fatalf("Format = %q, want a %s: prefix", printable, sha256hash.Name)
	}
	for _, in := range []string{printable, hex.EncodeToString(d)} {
		back, err := sha256hash.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", in, err)
		}
		if !back.Equal(d) {
			t.Fatalf("Parse(%q) = %s, want %s", in, back, d)
		}
	}
	if got := sha256hash.Format(nil); got != "" {
		t.Fatalf("Format(absent) = %q, want \"\"", got)
	}
}

// TestParseRejects pins the rejections: another algorithm's prefix, malformed
// hex, and a wrong width are all ErrInvalidDigest.
func TestParseRejects(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	for _, in := range []string{
		"",
		"sha1:" + strings.Repeat("ab", 20),
		"sha256:" + strings.Repeat("ab", 31),
		"sha256:" + strings.Repeat("ab", 33),
		"sha256:zz",
		"sha256:" + strings.ToUpper(valid),
		"nope",
	} {
		if _, err := sha256hash.Parse(in); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Parse(%q) = %v, want ErrInvalidDigest", in, err)
		}
	}
}

// TestShort pins the display form: 8 hex characters, or "<absent>".
func TestShort(t *testing.T) {
	d := sha256hash.Of([]byte("shorten me"))
	if got := sha256hash.Short(d); len(got) != 8 || !strings.HasPrefix(d.String(), got) {
		t.Fatalf("Short = %q, want the first 8 hex chars of %s", got, d)
	}
	if got := sha256hash.Short(nil); got != "<absent>" {
		t.Fatalf("Short(absent) = %q", got)
	}
}
