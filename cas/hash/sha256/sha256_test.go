package sha256_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256hash "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512hash "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512_256hash "github.com/dmundt/go-cask/cas/hash/sha512_256"
	adler32 "github.com/dmundt/go-cask/cas/verify/adler32"
	crc32 "github.com/dmundt/go-cask/cas/verify/crc32"
	crc64 "github.com/dmundt/go-cask/cas/verify/crc64"
)

// digestBytesEqual reports whether two digests hold the same bytes. It does not
// use Digest.Equal, which reports false when either side is absent and so cannot
// compare two digests a hasher just produced.
func digestBytesEqual(a, b cas.Digest) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDigestOwnsItsBytes pins what every shipped hasher relies on and what must
// stay true when its Digest fills a fixed-width buffer instead of a fresh one:
// the returned bytes are NewDigest's copy, so they are the right bytes for the
// input, they survive the next call, and the computation is deterministic.
func TestDigestOwnsItsBytes(t *testing.T) {
	for _, hasher := range []struct {
		name  string
		width int
		want  string
		hash  cas.Hasher
	}{
		{
			name:  "sha256",
			width: 32,
			want:  "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
			hash:  sha256hash.New(),
		},
		{
			name:  "sha512",
			width: 64,
			want:  "1f40fc92da241694750979ee6cf582f2d5d7d28e18335de05abc54d0560e0f5302860c652bf08d560252aa5e74210546f369fbbbce8c12cfc7957b2652fe9a75",
			hash:  sha512hash.New(),
		},
		{
			name:  "sha512_256",
			width: 32,
			want:  "455e518824bc0601f9fb858ff5c37d417d67c2f8e0df2babe4808858aea830f8",
			hash:  sha512_256hash.New(),
		},
		{name: "crc32", width: 4, want: "e8b7be43", hash: crc32.New()},
		{name: "crc64", width: 8, want: "330284772e652b05", hash: crc64.New()},
		{name: "adler32", width: 4, want: "00620062", hash: adler32.New()},
	} {
		t.Run(hasher.name, func(t *testing.T) {
			first, err := hasher.hash.Digest(strings.NewReader("a"))
			if err != nil {
				t.Fatal(err)
			}
			firstCopy := first.Bytes()
			second, err := hasher.hash.Digest(strings.NewReader("b"))
			if err != nil {
				t.Fatal(err)
			}
			if len(first) != hasher.width || len(second) != hasher.width {
				t.Fatalf("widths = %d and %d, want %d", len(first), len(second), hasher.width)
			}
			if got := hex.EncodeToString(first); got != hasher.want {
				t.Errorf("Digest(%q) = %s, want %s", "a", got, hasher.want)
			}
			if digestBytesEqual(first, second) {
				t.Fatalf("Digest(%q) = Digest(%q) = %x, want distinct digests", "a", "b", first)
			}
			if !digestBytesEqual(first, firstCopy) {
				t.Errorf("the first digest changed after a second Digest call: %x, want %x", first, firstCopy)
			}
			again, err := hasher.hash.Digest(strings.NewReader("a"))
			if err != nil {
				t.Fatal(err)
			}
			if !digestBytesEqual(again, firstCopy) {
				t.Errorf("Digest(%q) = %x, want %x (deterministic across calls)", "a", again, firstCopy)
			}
		})
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
