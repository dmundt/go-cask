package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --- Golden / NIST vectors (testing-strategy §4.6) ---

func TestGoldenVectors(t *testing.T) {
	cases := []struct{ input, want string }{
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tc := range cases {
		if got := HashBytes([]byte(tc.input)).String(); got != SHA256+":"+tc.want {
			t.Errorf("HashBytes(%q) = %q, want %q", tc.input, got, SHA256+":"+tc.want)
		}
	}
}

// --- CAS law: determinism (testing-strategy §1) ---

func TestHashDeterminism(t *testing.T) {
	data := []byte("same bytes every time")
	h1 := HashBytes(data)
	h2 := HashBytes(data)
	if h1.String() != h2.String() {
		t.Fatalf("determinism broken: %s != %s", h1, h2)
	}
	if !h1.Equal(h2) || !h2.Equal(h1) {
		t.Fatal("Equal must be reflexive for identical hashes")
	}
}

// --- ParseHash (cas-core §4.1; testing-strategy §3 inventory) ---

func TestParseHashValid(t *testing.T) {
	cases := []string{
		"sha256:" + strings.Repeat("ab", 32),
		SHA256 + ":" + strings.Repeat("00", 32),
	}
	for _, s := range cases {
		h, err := ParseHash(s)
		if err != nil {
			t.Errorf("ParseHash(%q): %v", s, err)
			continue
		}
		if h.String() != s {
			t.Errorf("round-trip: %q -> %q", s, h.String())
		}
		if h.Algorithm() != SHA256 {
			t.Errorf("Algorithm() = %q, want %q", h.Algorithm(), SHA256)
		}
	}
}

func TestParseHashInvalid(t *testing.T) {
	validDigest := strings.Repeat("ab", 32)
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrInvalidHash},                                       // empty
		{"sha256", ErrInvalidHash},                                 // no colon
		{":ab", ErrInvalidHash},                                    // empty algo
		{"sha256:", ErrInvalidHash},                                // empty digest
		{"sha256:" + strings.Repeat("ab", 31), ErrInvalidHash},     // too short
		{"sha256:" + strings.Repeat("ab", 33), ErrInvalidHash},     // too long
		{"sha256:" + strings.Repeat("a", 63), ErrInvalidHash},      // odd-length hex
		{"sha256:" + strings.ToUpper(validDigest), ErrInvalidHash}, // uppercase digest
		{"SHA256:" + validDigest, ErrInvalidHash},                  // uppercase algo name
		{"sha256:zz" + validDigest[2:], ErrInvalidHash},            // non-hex
		{"sha3:" + validDigest, ErrUnknownAlgorithm},               // algorithm not in this build
		{"sha1:" + strings.Repeat("ab", 20), ErrUnknownAlgorithm},  // a legacy address shape
	}
	for _, tc := range cases {
		_, err := ParseHash(tc.in)
		if !errors.Is(err, tc.want) {
			t.Errorf("ParseHash(%q) error = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestNewHash(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	digest := make([]byte, len(sum))
	copy(digest, sum[:])
	h, err := NewHash(digest)
	if err != nil {
		t.Fatal(err)
	}
	if want := SHA256 + ":" + hex.EncodeToString(digest); h.String() != want {
		t.Errorf("NewHash string = %q, want %q", h.String(), want)
	}
	// Must not alias the input slice (immutability).
	digest[0] = 99
	if h.String() != SHA256+":"+hex.EncodeToString(sum[:]) {
		t.Errorf("NewHash aliased its input: %q", h.String())
	}

	// With one algorithm the digest width is fixed: any other width cannot name
	// a stored object.
	for _, bad := range [][]byte{nil, {}, {1, 2, 3, 4}, make([]byte, sha256.Size+1)} {
		if _, err := NewHash(bad); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("NewHash(%d bytes) error = %v, want ErrInvalidHash", len(bad), err)
		}
	}
}

// --- Equal semantics ---

func TestHashEqual(t *testing.T) {
	a, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	b, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	c, _ := ParseHash("sha256:" + strings.Repeat("cd", 32))
	if !a.Equal(b) || !b.Equal(a) {
		t.Error("identical hashes must be equal")
	}
	if a.Equal(c) || c.Equal(a) {
		t.Error("different digests must not be equal")
	}
	// An absent address equals nothing, including another absent one.
	var absent Hash
	if a.Equal(absent) || absent.Equal(a) || absent.Equal(absent) {
		t.Error("the absent hash must not compare equal")
	}
}

func TestBytesIsCopy(t *testing.T) {
	h, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	b := h.Bytes()
	b[0] = 0xff
	if h.Bytes()[0] == 0xff {
		t.Fatal("Bytes() must not alias internal state")
	}
}

// TestHashZeroValue pins the absent hash: it is the zero value, it reports
// IsZero, it renders as "" rather than a bare ":", and it equals nothing.
func TestHashZeroValue(t *testing.T) {
	var h Hash
	if !h.IsZero() {
		t.Fatal("the zero Hash must be absent")
	}
	if h.String() != "" || h.Algorithm() != "" || h.Bytes() != nil {
		t.Fatalf("zero Hash renders as %q / %q / %v", h.String(), h.Algorithm(), h.Bytes())
	}
	if h.Equal(h) {
		t.Fatal("an absent hash must not equal itself")
	}

	// CheckHash is the guard the store and the backends apply.
	if err := CheckHash(Hash{}, "test"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("CheckHash(zero) = %v, want ErrInvalidHash", err)
	}
	present, err := ParseHash("sha256:" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckHash(present, "test"); err != nil {
		t.Fatalf("CheckHash(present) = %v", err)
	}
}

// TestHashHasNoJSON pins that the core stays free of any encoding: the JSON
// field shape lives in the JSON codec (jsoncodec.Hash), so a bare Hash field
// does not serialize as "algo:hexdigest" — object types use the codec type.
func TestHashHasNoJSON(t *testing.T) {
	h, err := ParseHash("sha256:" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{}` {
		t.Fatalf("json.Marshal(cas.Hash) = %s, want {} (unexported fields; use jsoncodec.Hash in fields)", raw)
	}
	var back Hash
	if err := json.Unmarshal([]byte(`"`+h.String()+`"`), &back); err == nil {
		t.Fatalf("json.Unmarshal into a bare Hash must fail, got %v", back)
	}
}

// ExampleParseHash demonstrates the canonical hash string form.
func ExampleParseHash() {
	h, err := ParseHash("sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	if err != nil {
		panic(err)
	}
	_ = h.Algorithm() // "sha256"
}

// FuzzParseHash must never panic and must round-trip valid output.
func FuzzParseHash(f *testing.F) {
	f.Add("sha256:" + strings.Repeat("ab", 32))
	f.Add("sha256:" + strings.Repeat("ab", 31))
	f.Add("sha1:" + strings.Repeat("ab", 20))
	f.Add("garbage")
	f.Add("sha256:")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		h, err := ParseHash(s)
		if err != nil {
			return
		}
		// Valid output must round-trip through String() and ParseHash.
		h2, err := ParseHash(h.String())
		if err != nil {
			t.Fatalf("round-trip of %q failed: %v", h.String(), err)
		}
		if !h.Equal(h2) {
			t.Fatalf("round-trip changed hash: %s vs %s", h, h2)
		}
	})
}

func TestHashBytes(t *testing.T) {
	data := []byte("hash bytes")
	got := HashBytes(data)
	want := sha256.Sum256(data)
	if got.String() != SHA256+":"+hex.EncodeToString(want[:]) {
		t.Fatalf("HashBytes = %s, want sha256:%s", got, hex.EncodeToString(want[:]))
	}
	if got.Algorithm() != SHA256 {
		t.Fatalf("HashBytes algorithm = %q, want %q", got.Algorithm(), SHA256)
	}
}

func TestNewHasher(t *testing.T) {
	h := NewHasher()
	h.Write([]byte("abc"))
	streamed, err := NewHash(h.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !streamed.Equal(HashBytes([]byte("abc"))) {
		t.Fatal("streaming hash mismatch")
	}
}

func TestHashBytesRoundTrip(t *testing.T) {
	// HashBytes output parses back into the same hash.
	h := HashBytes([]byte("round trip"))
	back, err := ParseHash(h.String())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Equal(back) {
		t.Fatal("round-trip mismatch")
	}
	if !strings.HasPrefix(h.String(), SHA256+":") {
		t.Fatalf("hash = %q", h.String())
	}
}
