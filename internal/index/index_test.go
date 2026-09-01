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

func TestEnvelopeType(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"data":"aGk="}`))
	env, _ := json.Marshal(map[string]string{"type": "blob@1", "data": payload})
	if got := EnvelopeType(env); got != "blob@1" {
		t.Fatalf("EnvelopeType = %q, want blob@1", got)
	}
	if got := EnvelopeType([]byte("not an envelope")); got != "" {
		t.Fatalf("EnvelopeType(raw) = %q, want empty", got)
	}
}
