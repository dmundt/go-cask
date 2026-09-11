package sha512_256_test

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha512_256hash "github.com/dmundt/go-cask/cas/hash/sha512_256"
)

// TestOfMatchesStdlib pins the digest bytes against crypto/sha512.
func TestOfMatchesStdlib(t *testing.T) {
	data := []byte("hash me")
	sum := sha512.Sum512_256(data)
	got := sha512_256hash.Of(data)
	if hex.EncodeToString(got) != hex.EncodeToString(sum[:]) {
		t.Fatalf("Of = %s, want %s", got, hex.EncodeToString(sum[:]))
	}
	if len(got) != sha512_256hash.Size {
		t.Fatalf("Of returned %d bytes, want %d", len(got), sha512_256hash.Size)
	}
}

// TestHasherDigestStreams pins the Hasher seam: Digest reads a whole stream and
// agrees with Of.
func TestHasherDigestStreams(t *testing.T) {
	data := []byte(strings.Repeat("stream", 100))
	got, err := sha512_256hash.New().Digest(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(sha512_256hash.Of(data)) {
		t.Fatalf("Digest = %s, want %s", got, sha512_256hash.Of(data))
	}
}

// TestHasherValidate pins the width check that keeps a key of the wrong size
// out of a store.
func TestHasherValidate(t *testing.T) {
	h := sha512_256hash.New()
	if err := h.Validate(sha512_256hash.Of([]byte("x"))); err != nil {
		t.Fatalf("Validate(present) = %v", err)
	}
	for _, d := range []cas.Digest{nil, cas.NewDigest([]byte{1, 2, 3}), cas.NewDigest(make([]byte, 64))} {
		if err := h.Validate(d); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Validate(%d bytes) = %v, want ErrInvalidDigest", len(d), err)
		}
	}
}

// TestNewHasherMatchesOf pins the streaming hash.Hash path against the one-shot
// helper.
func TestNewHasherMatchesOf(t *testing.T) {
	data := []byte("stream and spool")
	hh := sha512_256hash.NewHasher()
	hh.Write(data)
	if got := hex.EncodeToString(hh.Sum(nil)); got != hex.EncodeToString(sha512_256hash.Of(data)) {
		t.Fatalf("NewHasher sum = %s", got)
	}
}

// TestFormatParse pins the printable form the clients use in URLs, CLI
// arguments and JSON responses.
func TestFormatParse(t *testing.T) {
	d := sha512_256hash.Of([]byte("printable"))
	printable := sha512_256hash.Format(d)
	if !strings.HasPrefix(printable, sha512_256hash.Name+":") {
		t.Fatalf("Format = %q, want a %s: prefix", printable, sha512_256hash.Name)
	}
	for _, in := range []string{printable, hex.EncodeToString(d)} {
		back, err := sha512_256hash.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", in, err)
		}
		if !back.Equal(d) {
			t.Fatalf("Parse(%q) = %s, want %s", in, back, d)
		}
	}
	if got := sha512_256hash.Format(nil); got != "" {
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
		"sha512_256:" + strings.Repeat("ab", 31),
		"sha512_256:" + strings.Repeat("ab", 33),
		"sha512_256:zz",
		"sha512_256:" + strings.ToUpper(valid),
		"nope",
	} {
		if _, err := sha512_256hash.Parse(in); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Parse(%q) = %v, want ErrInvalidDigest", in, err)
		}
	}
}
