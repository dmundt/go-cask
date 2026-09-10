package cas

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ExampleHashRef shows the field pattern: a value cas.HashRef serializes as its
// canonical "algo:hexdigest" string, `omitzero` drops an absent reference from
// the encoding, and a malformed one is rejected at decode rather than silently
// becoming absent.
func ExampleHashRef() {
	type Commit struct {
		Tree   HashRef `json:"tree"`            // required
		Parent HashRef `json:"parent,omitzero"` // absent -> omitted
	}

	tree, err := HashBytes("sha256", []byte("tree"))
	if err != nil {
		panic(err)
	}
	encoded, err := json.Marshal(Commit{Tree: NewHashRef(tree)})
	if err != nil {
		panic(err)
	}
	fmt.Println("encodes the canonical form:", strings.HasPrefix(string(encoded), `{"tree":"sha256:`))
	fmt.Println("omits the absent parent:", !strings.Contains(string(encoded), "parent"))

	var back Commit
	if err := json.Unmarshal(encoded, &back); err != nil {
		panic(err)
	}
	fmt.Println("absent parent decodes absent:", back.Parent.IsZero())
	fmt.Println("tree round-trips:", back.Tree.Hash().Equal(tree))

	var bad Commit
	fmt.Println("malformed reference rejected:", json.Unmarshal([]byte(`{"tree":"nope:zz"}`), &bad) != nil)

	// Output:
	// encodes the canonical form: true
	// omits the absent parent: true
	// absent parent decodes absent: true
	// tree round-trips: true
	// malformed reference rejected: true
}

// TestHashRefJSON pins the contract that lets object types drop their JSON
// code: a value field with `omitzero` is omitted when absent, a value field
// without it encodes the historical "", and slices work element-wise.
func TestHashRefJSON(t *testing.T) {
	h, err := ParseHash("sha256:" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	present := `"` + h.String() + `"`

	type holder struct {
		Req     HashRef   `json:"req"`             // required: absent encodes as ""
		Opt     HashRef   `json:"opt,omitzero"`    // optional: absent is omitted
		Slice   []HashRef `json:"slice,omitempty"` // optional: empty slice omitted
		NoOmitZ HashRef   `json:"noz,omitempty"`   // omitempty cannot omit a struct
	}

	// A present reference in every field shape.
	raw, err := json.Marshal(holder{
		Req:     NewHashRef(h),
		Opt:     NewHashRef(h),
		Slice:   []HashRef{NewHashRef(h)},
		NoOmitZ: NewHashRef(h),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"req":` + present + `,"opt":` + present + `,"slice":[` + present + `],"noz":` + present + `}`
	if string(raw) != want {
		t.Fatalf("marshal = %s, want %s", raw, want)
	}

	// Absent: omitzero drops the optional field, the required field keeps "".
	raw, err = json.Marshal(holder{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"req":"","noz":""}` {
		t.Fatalf("absent marshal = %s, want {\"req\":\"\",\"noz\":\"\"}", raw)
	}

	// Unmarshal populates every shape without object-type code.
	var back holder
	if err := json.Unmarshal([]byte(`{"req":`+present+`,"opt":`+present+`,"slice":[`+present+`]}`), &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Req.Hash(); got == nil || !got.Equal(h) {
		t.Fatalf("required field = %v", got)
	}
	if got := back.Opt.Hash(); got == nil || !got.Equal(h) {
		t.Fatalf("optional field = %v", got)
	}
	if len(back.Slice) != 1 || !back.Slice[0].Hash().Equal(h) {
		t.Fatalf("slice field = %v", back.Slice)
	}

	// "" and null both mean absent (no error) — the historical behavior of an
	// optional reference, and what keeps a root commit's omitted parent absent.
	for _, in := range []string{`{"req":""}`, `{"req":null}`, `{}`} {
		var v holder
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("Unmarshal(%s) = %v, want absent", in, err)
		}
		if !v.Req.IsZero() || !v.Opt.IsZero() || !v.NoOmitZ.IsZero() {
			t.Fatalf("Unmarshal(%s) not absent", in)
		}
	}

	// A malformed or unknown-algorithm reference fails decode, so a stored
	// object can never carry a reference that References() would silently drop.
	for _, in := range []string{
		`{"req":"nope:zz"}`,
		`{"req":"sha256:not-hex"}`,
		`{"req":42}`,
		`{"req":{"nested":true}}`,
	} {
		var v holder
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Fatalf("Unmarshal(%s) must fail", in)
		}
	}

	// The absent value round-trips as absent rather than as a zero digest.
	raw, err = json.Marshal(holder{Req: NewHashRef(nil)})
	if err != nil {
		t.Fatal(err)
	}
	var again holder
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if !again.Req.IsZero() {
		t.Fatalf("absent did not round-trip: %v", again.Req.Hash())
	}
}

// TestHashRefZeroValue pins that the zero value is absent and IsZero reports it
// — the pair encoding/json's omitzero relies on.
func TestHashRefZeroValue(t *testing.T) {
	var r HashRef
	if !r.IsZero() || r.Hash() != nil {
		t.Fatalf("zero HashRef: IsZero=%v Hash=%v, want absent", r.IsZero(), r.Hash())
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`""`), &r); err != nil {
		t.Fatal(err)
	}
	if !r.IsZero() {
		t.Fatalf("empty string decoded to a present reference: %v", r.Hash())
	}
	// A non-string, non-null JSON value is a decode error, not a silent absent.
	if err := json.Unmarshal([]byte(`[]`), &r); err == nil {
		t.Fatal("array into HashRef must fail")
	}
	if !r.IsZero() {
		t.Fatalf("failed decode left a value: %v", r.Hash())
	}
}
