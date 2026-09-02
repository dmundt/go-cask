package index

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestPaginate(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5}
	cases := []struct {
		offset, limit int
		want          []int
	}{
		{0, 100, items},
		{0, 2, []int{0, 1}},
		{2, 2, []int{2, 3}},
		{10, 2, []int{}},     // offset beyond end
		{-1, 2, []int{0, 1}}, // negative offset clamped
		{4, 100, []int{4, 5}},
	}
	for _, tc := range cases {
		got := Paginate(items, tc.offset, tc.limit)
		if len(got) != len(tc.want) {
			t.Fatalf("Paginate(%d,%d) = %v, want %v", tc.offset, tc.limit, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Paginate(%d,%d) = %v, want %v", tc.offset, tc.limit, got, tc.want)
			}
		}
	}
}

// TestEnvelopeType pins the best-effort envelope sniffing contract: the
// versioned type name is returned verbatim when the bytes are a JSON object
// with a non-empty string "type"; "" otherwise (raw objects have no type).
func TestEnvelopeType(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"data":"aGk="}`))
	env, _ := json.Marshal(map[string]string{"type": "blob@1", "data": payload})
	versioned := []byte(`{"type":"blob@1","data":"aGk="}`)
	raw := []byte("not an envelope")

	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"versioned type returned verbatim", env, "blob@1"},
		{"bare type without version", []byte(`{"type":"blob","data":"aGk="}`), "blob"},
		{"missing type key", []byte(`{"data":"aGk="}`), ""},
		{"empty type", []byte(`{"type":"","data":"aGk="}`), ""},
		{"non-string type", []byte(`{"type":5}`), ""},
		{"json array is not an envelope", []byte(`[1,2]`), ""},
		{"garbage bytes", raw, ""},
		{"empty input", nil, ""},
		{"versioned without data payload still typed", versioned, "blob@1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnvelopeType(tc.in); got != tc.want {
				t.Fatalf("EnvelopeType(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
