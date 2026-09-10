package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// hashData computes the content address of data with the named algorithm.
// Test helper only — production code uses the public HashBytes; see cas-core
// §4.2 for the registry contract.
func hashData(algo string, data []byte) (Hash, error) {
	fn, ok := LookupHash(algo)
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
	RegisterHash("equalalgo", func(data []byte) Hash {
		return hash{algo: "equalalgo", bytes: []byte{0x01}}
	})
	a, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	b, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))
	c, _ := ParseHash("sha256:" + strings.Repeat("cd", 32))
	// Same digest, different algorithm — Equal must compare the algorithm too.
	d, err := NewHash("equalalgo", a.Bytes())
	if err != nil {
		t.Fatal(err)
	}
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

// TestRegisterHashOverridesStreamHasher pins registry parity: registering a
// one-shot function for a name that also has a streaming hasher drops the
// stream constructor, so HashBytes and NewHasher never disagree about the
// address of one algorithm name.
func TestRegisterHashOverridesStreamHasher(t *testing.T) {
	RegisterHash("parityalgo", func(data []byte) Hash {
		return hash{algo: "parityalgo", bytes: []byte{0x01}}
	})
	registerStreamHash("parityalgo", sha256.New)
	RegisterHash("parityalgo", func(data []byte) Hash {
		return hash{algo: "parityalgo", bytes: []byte{0x02}}
	})
	if _, err := NewHasher("parityalgo"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(parityalgo) = %v, want ErrUnknownAlgorithm after re-registration", err)
	}
	got, err := HashBytes("parityalgo", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "parityalgo:02" {
		t.Fatalf("HashBytes(parityalgo) = %q, want the re-registered one-shot result", got)
	}
}

// TestRegisterHashRejectsInvalidName pins the name validation that keeps a
// hostile algorithm name out of store paths.
func TestRegisterHashRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", "..", "../evil", "SHA256", "a/b", "a b"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("RegisterHash(%q) must panic", name)
				}
			}()
			RegisterHash(name, func([]byte) Hash { return nil })
		})
	}
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterHash with a nil func must panic")
		}
	}()
	RegisterHash("nilfunc", nil)
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

// TestHashJSONMarshal pins the Hash JSON contract: a Hash marshals as its
// canonical "algo:hexdigest" string through interface fields and slices, which
// is what lets object types declare plain Hash fields (gitlike's
// TreeEntry/Commit/Tag). Unmarshalling into a Hash field stays impossible for
// encoding/json — interface with no exported implementation — which is why
// those types keep their own UnmarshalJSON.
func TestHashJSONMarshal(t *testing.T) {
	h, err := ParseHash("sha256:" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	want := `"` + h.String() + `"`

	direct, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(direct) != want {
		t.Fatalf("json.Marshal(Hash) = %s, want %s", direct, want)
	}

	type holder struct {
		Tree  Hash   `json:"tree"`
		Refs  []Hash `json:"refs,omitempty"`
		Empty Hash   `json:"empty,omitempty"`
	}
	got, err := json.Marshal(holder{Tree: h, Refs: []Hash{h, h}})
	if err != nil {
		t.Fatal(err)
	}
	wantHolder := `{"tree":` + want + `,"refs":[` + want + `,` + want + `]}`
	if string(got) != wantHolder {
		t.Fatalf("json.Marshal(holder) = %s, want %s", got, wantHolder)
	}

	// A nil Hash is omitted under omitempty, and null without it.
	got, err = json.Marshal(struct {
		Empty Hash `json:"empty"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"empty":null}` {
		t.Fatalf("nil Hash = %s, want {\"empty\":null}", got)
	}

	// Decoding into a Hash field cannot work: documented reason for keeping
	// per-type UnmarshalJSON in the object model.
	if err := json.Unmarshal([]byte(`{"tree":`+want+`}`), &holder{}); err == nil {
		t.Fatal("json.Unmarshal into a Hash field must fail (no exported implementation)")
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

func TestHashBytes(t *testing.T) {
	data := []byte("hash bytes")
	got, err := HashBytes("sha256", data)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hashData("sha256", data)
	if got.String() != want.String() {
		t.Fatalf("HashBytes = %s, want %s", got, want)
	}
	// One-shot custom algorithms (no stream constructor) use the HashFunc
	// fallback path.
	if _, err := HashBytes("nope", data); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("unknown algo = %v, want ErrUnknownAlgorithm", err)
	}
}

// A one-shot registered algorithm (no streaming hasher) exercises the
// HashBytes fallback.
func TestHashBytesOneShotFallback(t *testing.T) {
	RegisterHash("oneshot", func(data []byte) Hash {
		return hash{algo: "oneshot", bytes: []byte{0x01}}
	})
	h, err := HashBytes("oneshot", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "oneshot:01" {
		t.Fatalf("HashBytes(oneshot) = %q", h.String())
	}
}

func TestNewHasher(t *testing.T) {
	h, err := NewHasher("sha256")
	if err != nil {
		t.Fatal(err)
	}
	h.Write([]byte("abc"))
	sum := h.Sum(nil)
	expected, _ := hashData("sha256", []byte("abc"))
	if string(sum) != string(expected.Bytes()) {
		t.Fatal("streaming hash mismatch")
	}
	// A one-shot algorithm cannot stream.
	if _, err := NewHasher("oneshot"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(oneshot) = %v, want ErrUnknownAlgorithm", err)
	}
	if _, err := NewHasher("nope"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(nope) = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestHashBytesRoundTrip(t *testing.T) {
	// HashBytes output parses back into the same hash.
	h, _ := HashBytes("sha256", []byte("round trip"))
	back, err := ParseHash(h.String())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Equal(back) {
		t.Fatal("round-trip mismatch")
	}
	if !strings.HasPrefix(h.String(), "sha256:") {
		t.Fatalf("hash = %q", h.String())
	}
}

// TestHashOneShotRegistration pins the one-shot-only hash paths: HashBytes
// works through the registry, NewHasher rejects non-streamable algorithms.
func TestHashOneShotRegistration(t *testing.T) {
	RegisterHash("obone", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		h, _ := NewHash("obone", sum[:])
		return h
	})
	want := sha256.Sum256([]byte("abc"))
	h, err := HashBytes("obone", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "obone:"+hex.EncodeToString(want[:]) {
		t.Fatalf("HashBytes = %q", h.String())
	}
	if _, err := NewHasher("obone"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(one-shot) err = %v, want ErrUnknownAlgorithm", err)
	}
	if hs, err := NewHasher("sha256"); err != nil || hs == nil {
		t.Fatalf("NewHasher(sha256) = %v, %v", hs, err)
	}
}
