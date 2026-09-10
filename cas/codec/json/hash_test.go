package json_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// TestHashJSON pins the contract that lets object types carry no JSON code: a
// jsoncodec.Hash field marshals as the canonical "algo:hexdigest" string, the
// absent value as "", and an `omitzero` field is dropped when it is absent.
func TestHashJSON(t *testing.T) {
	h, err := cas.ParseHash("sha256:" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	want := `"` + h.String() + `"`

	direct, err := json.Marshal(jsoncodec.NewHash(h))
	if err != nil {
		t.Fatal(err)
	}
	if string(direct) != want {
		t.Fatalf("json.Marshal(Hash) = %s, want %s", direct, want)
	}

	type holder struct {
		Tree  jsoncodec.Hash   `json:"tree"`            // required: absent encodes as ""
		Refs  []jsoncodec.Hash `json:"refs,omitempty"`  // optional: empty slice omitted
		Opt   jsoncodec.Hash   `json:"opt,omitzero"`    // optional: absent omitted
		Empty jsoncodec.Hash   `json:"empty,omitempty"` // omitempty cannot omit a struct
	}
	got, err := json.Marshal(holder{
		Tree:  jsoncodec.NewHash(h),
		Refs:  []jsoncodec.Hash{jsoncodec.NewHash(h), jsoncodec.NewHash(h)},
		Opt:   jsoncodec.NewHash(h),
		Empty: jsoncodec.NewHash(h),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantHolder := `{"tree":` + want + `,"refs":[` + want + `,` + want + `],"opt":` + want + `,"empty":` + want + `}`
	if string(got) != wantHolder {
		t.Fatalf("json.Marshal(holder) = %s, want %s", got, wantHolder)
	}

	// Absent: omitzero drops the optional field, the required field keeps "".
	got, err = json.Marshal(holder{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"tree":"","empty":""}` {
		t.Fatalf("absent marshal = %s, want {\"tree\":\"\",\"empty\":\"\"}", got)
	}

	// Decoding fills every shape; "" and null decode to the absent reference.
	var back holder
	if err := json.Unmarshal([]byte(`{"tree":`+want+`,"refs":[`+want+`],"opt":`+want+`}`), &back); err != nil {
		t.Fatal(err)
	}
	if !back.Tree.Hash().Equal(h) || !back.Opt.Hash().Equal(h) || len(back.Refs) != 1 || !back.Refs[0].Hash().Equal(h) {
		t.Fatalf("decoded holder = %+v", back)
	}
	for _, in := range []string{`{"tree":""}`, `{"tree":null}`, `{}`} {
		var v holder
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("Unmarshal(%s) = %v, want absent", in, err)
		}
		if !v.Tree.IsZero() || !v.Opt.IsZero() || !v.Tree.Hash().IsZero() {
			t.Fatalf("Unmarshal(%s) not absent", in)
		}
	}

	// A malformed or unknown-algorithm reference fails decode, so a stored
	// object can never carry a reference that References() would silently drop.
	for _, in := range []string{
		`{"tree":"nope:zz"}`,
		`{"tree":"sha256:not-hex"}`,
		`{"tree":42}`,
		`{"tree":{"nested":true}}`,
	} {
		var v holder
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Fatalf("Unmarshal(%s) must fail", in)
		}
	}

	// The zero value is the absent reference and round-trips as absent.
	var zero jsoncodec.Hash
	if !zero.IsZero() || !zero.Hash().IsZero() {
		t.Fatalf("zero Hash: IsZero=%v wrapped=%v", zero.IsZero(), zero.Hash())
	}
	raw, err := json.Marshal(holder{Tree: zero})
	if err != nil {
		t.Fatal(err)
	}
	var again holder
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if !again.Tree.Hash().IsZero() {
		t.Fatalf("absent did not round-trip: %v", again.Tree.Hash())
	}
}

// ExampleHash shows the field pattern: a jsoncodec.Hash field serializes as its
// canonical "algo:hexdigest" string, `omitzero` drops an absent reference from
// the encoding, and a malformed one is rejected at decode rather than silently
// becoming absent.
func ExampleHash() {
	type Commit struct {
		Tree   jsoncodec.Hash `json:"tree"`            // required
		Parent jsoncodec.Hash `json:"parent,omitzero"` // absent -> omitted
	}

	tree := cas.HashBytes([]byte("tree"))
	encoded, err := json.Marshal(Commit{Tree: jsoncodec.NewHash(tree)})
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
