package cas

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// hashData computes the content address of data with the named algorithm.
// Test helper only — production code uses the public HashBytes; see cas-core
// §4.2 for the registry contract.
func hashData(algo string, data []byte) (Hash, error) {
	fn, ok := lookupHash(algo)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algo)
	}
	return fn(data), nil
}

// --- Golden / NIST vectors (testing-strategy §4.6) ---

func TestGoldenVectors(t *testing.T) {
	cases := []struct {
		algo, input, want string
	}{
		{"sha256", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"sha256", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tc := range cases {
		h, err := hashData(tc.algo, []byte(tc.input))
		if err != nil {
			t.Fatalf("%s(%q): %v", tc.algo, tc.input, err)
		}
		if got := h.String(); got != tc.algo+":"+tc.want {
			t.Errorf("%s(%q) = %q, want %q", tc.algo, tc.input, got, tc.algo+":"+tc.want)
		}
	}
}

// --- CAS law: determinism (testing-strategy §1) ---

func TestHashDeterminism(t *testing.T) {
	data := []byte("same bytes every time")
	h1, _ := hashData("sha256", data)
	h2, _ := hashData("sha256", data)
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
		"sha1:" + strings.Repeat("cd", 20),
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
		if h.Algorithm() != s[:strings.IndexByte(s, ':')] {
			t.Errorf("Algorithm() = %q", h.Algorithm())
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
		{"sha256:" + strings.Repeat("a", 31), ErrInvalidHash},      // odd-length hex
		{"sha256:" + strings.ToUpper(validDigest), ErrInvalidHash}, // uppercase
		{"SHA256:" + validDigest, ErrInvalidHash},                  // uppercase algo
		{"sha256:zz" + validDigest[2:], ErrInvalidHash},            // non-hex
		{"sha3:" + validDigest, ErrUnknownAlgorithm},               // unknown algo
	}
	for _, tc := range cases {
		_, err := ParseHash(tc.in)
		if !errors.Is(err, tc.want) {
			t.Errorf("ParseHash(%q) error = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestNewHash(t *testing.T) {
	digest := []byte{1, 2, 3, 4}
	h, err := NewHash("sha256", digest)
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "sha256:01020304" {
		t.Errorf("NewHash string = %q", h.String())
	}
	// Must not alias the input slice (immutability).
	digest[0] = 99
	if h.String() != "sha256:01020304" {
		t.Errorf("NewHash aliased its input: %q", h.String())
	}
	if _, err := NewHash("nope", digest); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Errorf("unknown algo: got %v", err)
	}
	if _, err := NewHash("sha256", nil); !errors.Is(err, ErrInvalidHash) {
		t.Errorf("empty digest: got %v", err)
	}
}

// --- Equal semantics ---

func TestHashEqual(t *testing.T) {
	a, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	b, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	c, _ := ParseHash("sha256:" + strings.Repeat("cd", 32))
	d, _ := ParseHash("sha1:" + strings.Repeat("ab", 20))
	if !a.Equal(b) || !b.Equal(a) {
		t.Error("identical hashes must be equal")
	}
	if a.Equal(c) || c.Equal(a) {
		t.Error("different digests must not be equal")
	}
	if a.Equal(d) || d.Equal(a) {
		t.Error("same digest different algorithm must not be equal")
	}
	if a.Equal(nil) {
		t.Error("hash must not equal nil")
	}
}

// --- RegisterHash (pluggable algorithms, cas-core §4.2) ---

func TestRegisterHash(t *testing.T) {
	RegisterHash("testalgo", func(data []byte) Hash {
		return hash{algo: "testalgo", bytes: []byte{0xde, 0xad}}
	})
	h, err := ParseHash("testalgo:dead")
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "testalgo:dead" {
		t.Errorf("custom algo string = %q", h.String())
	}
	// HashFunc is deterministic and callable through the registry.
	hr, err := hashData("testalgo", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if hr.String() != "testalgo:dead" {
		t.Errorf("custom HashFunc = %q", hr.String())
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
