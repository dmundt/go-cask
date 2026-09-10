package json

import (
	"encoding/json"
	"fmt"

	"github.com/dmundt/go-cask/cas"
)

// Hash is a cas.Hash in the shape a JSON object field needs: it renders as the
// canonical "algo:hexdigest" string, may be absent, and validates on decode, so
// an object type declares plain value fields and carries no JSON code of its
// own (cas-core §4.2):
//
//	type Commit struct {
//		Tree   jsoncodec.Hash `json:"tree"`            // required
//		Parent jsoncodec.Hash `json:"parent,omitzero"` // absent -> omitted
//	}
//
// The zero value is the absent reference, so an optional field can simply be
// left out of a struct literal; build a present one with NewHash, and unwrap
// with Hash, which returns the zero cas.Hash when absent.
//
// `omitzero` is what lets the value shape cover optional references: it omits
// the field when IsZero reports absent, whereas `omitempty` cannot omit a
// struct. That is also why the module declares `go 1.24` — an older standard
// library silently ignores the unknown tag option, which would change the
// stored bytes and therefore the object's address.
type Hash struct {
	h cas.Hash
}

// NewHash wraps h as a JSON object field. A zero h is allowed and means absent,
// so NewHash never fails: a cas.Hash is valid by construction (ParseHash/NewHash
// validate) and absence stays explicit.
func NewHash(h cas.Hash) Hash { return Hash{h: h} }

// Hash returns the wrapped cas.Hash, the zero value when the reference is
// absent.
func (f Hash) Hash() cas.Hash { return f.h }

// IsZero reports whether the reference is absent. It is the method
// encoding/json's `omitzero` option consults for a struct field.
func (f Hash) IsZero() bool { return f.h.IsZero() }

// MarshalJSON implements json.Marshaler: a present reference renders as its
// canonical "algo:hexdigest" string, an absent one as "". A field tagged
// `omitzero` never sees the absent case — the field is omitted instead.
func (f Hash) MarshalJSON() ([]byte, error) {
	if f.h.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(f.h.String())
}

// UnmarshalJSON implements json.Unmarshaler: "" and null mean absent, anything
// else must parse as a hash. A malformed or unknown-algorithm reference is
// rejected here (cas.ErrInvalidHash / cas.ErrUnknownAlgorithm), so an object
// read from a store can never hold an unparsable reference that would later
// "disappear" from References().
func (f *Hash) UnmarshalJSON(data []byte) error {
	var s *string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("cas: decode json hash field: %w", err)
	}
	if s == nil || *s == "" {
		f.h = cas.Hash{}
		return nil
	}
	h, err := cas.ParseHash(*s)
	if err != nil {
		return err
	}
	f.h = h
	return nil
}
