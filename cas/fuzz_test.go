package cas

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

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

// FuzzDigestJSONRoundTrip checks that a digest marshals to hex and unmarshals
// back without altering the underlying bytes.
func FuzzDigestJSONRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{{0x00}, {0x01, 0x02}, bytes.Repeat([]byte{0xab}, 32), nil} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		d := NewDigest(raw)
		payload, err := json.Marshal(struct {
			Ref Digest `json:"ref"`
		}{d})
		if err != nil {
			t.Fatalf("json.Marshal() = %v, want nil", err)
		}
		var back struct {
			Ref Digest `json:"ref"`
		}
		if err := json.Unmarshal(payload, &back); err != nil {
			t.Fatalf("json.Unmarshal(%s) = %v, want nil", payload, err)
		}
		if d.IsZero() && !back.Ref.IsZero() {
			t.Fatalf("absent digest must stay absent after JSON round-trip: got %s", back.Ref)
		}
		if !d.IsZero() && !d.Equal(back.Ref) {
			t.Fatalf("JSON round-trip changed digest: %s vs %s", d, back.Ref)
		}
	})
}
