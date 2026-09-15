package sha512_test

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha512hash "github.com/dmundt/go-cask/cas/hash/sha512"
)

func TestOfMatchesStdlib(t *testing.T) {
	data := []byte("hash me")
	sum := sha512.Sum512(data)
	got := sha512hash.Of(data)
	if hex.EncodeToString(got) != hex.EncodeToString(sum[:]) {
		t.Fatalf("Of = %s, want %s", got, hex.EncodeToString(sum[:]))
	}
	if len(got) != sha512hash.Size {
		t.Fatalf("Of returned %d bytes, want %d", len(got), sha512hash.Size)
	}
}

func TestHasherDigestStreams(t *testing.T) {
	data := []byte(strings.Repeat("stream", 100))
	got, err := sha512hash.New().Digest(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(sha512hash.Of(data)) {
		t.Fatalf("Digest = %s, want %s", got, sha512hash.Of(data))
	}
}

func TestHasherValidate(t *testing.T) {
	h := sha512hash.New()
	if err := h.Validate(sha512hash.Of([]byte("x"))); err != nil {
		t.Fatalf("Validate(present) = %v", err)
	}
	for _, d := range []cas.Digest{nil, cas.NewDigest([]byte{1, 2, 3}), cas.NewDigest(make([]byte, 63)), cas.NewDigest(make([]byte, 65))} {
		if err := h.Validate(d); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Validate(%d bytes) = %v, want ErrInvalidDigest", len(d), err)
		}
	}
}

func TestNewHasherMatchesOf(t *testing.T) {
	data := []byte("stream and spool")
	hh := sha512hash.NewHasher()
	hh.Write(data)
	if got := hex.EncodeToString(hh.Sum(nil)); got != hex.EncodeToString(sha512hash.Of(data)) {
		t.Fatalf("NewHasher sum = %s", got)
	}
}

func TestFormatParse(t *testing.T) {
	d := sha512hash.Of([]byte("printable"))
	printable := sha512hash.Format(d)
	if !strings.HasPrefix(printable, sha512hash.Name+":") {
		t.Fatalf("Format = %q, want a %s: prefix", printable, sha512hash.Name)
	}
	for _, in := range []string{printable, hex.EncodeToString(d)} {
		back, err := sha512hash.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", in, err)
		}
		if !back.Equal(d) {
			t.Fatalf("Parse(%q) = %s, want %s", in, back, d)
		}
	}
	if got := sha512hash.Format(nil); got != "" {
		t.Fatalf("Format(absent) = %q, want \"\"", got)
	}
}

func TestParseRejects(t *testing.T) {
	valid := strings.Repeat("ab", 64)
	for _, in := range []string{
		"",
		"sha1:" + strings.Repeat("ab", 20),
		"sha512:" + strings.Repeat("ab", 63),
		"sha512:" + strings.Repeat("ab", 65),
		"sha512:zz",
		"sha512:" + strings.ToUpper(valid),
		"nope",
	} {
		if _, err := sha512hash.Parse(in); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("Parse(%q) = %v, want ErrInvalidDigest", in, err)
		}
	}
}
