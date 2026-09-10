package cas

import (
	"encoding/json"
	"fmt"
)

// HashRef is a Hash in the shape an object field needs: it carries the
// canonical "algo:hexdigest" text form, may be absent, and serializes itself,
// so an object type declares a plain value field and needs no JSON code of its
// own (cas-core §4.2):
//
//	type Commit struct {
//		Tree   cas.HashRef `json:"tree"`            // required
//		Parent cas.HashRef `json:"parent,omitzero"` // absent -> omitted
//	}
//
// The zero value is absent, so an optional field can simply be left out of a
// struct literal; build a present one with NewHashRef (a nil Hash is allowed
// and means absent), and unwrap with Hash, which returns nil when absent.
//
// `omitzero` is what lets the value shape cover optional references: it omits
// the field when IsZero reports absent, whereas `omitempty` cannot omit a
// struct. That is also why the module declares `go 1.24` — an older standard
// library silently ignores the unknown tag option, which would change the
// stored bytes and therefore the object's address.
type HashRef struct {
	h Hash
}

// NewHashRef wraps h as an object field. A nil h is allowed and means absent,
// so NewHashRef never fails: a Hash value is valid by construction
// (NewHash/ParseHash validate) and absence stays explicit.
func NewHashRef(h Hash) HashRef { return HashRef{h: h} }

// Hash returns the wrapped Hash, or nil when the reference is absent.
func (r HashRef) Hash() Hash { return r.h }

// IsZero reports whether the reference is absent. It is the method
// encoding/json's `omitzero` option consults for a struct field.
func (r HashRef) IsZero() bool { return r.h == nil }

// MarshalJSON implements json.Marshaler: a present reference renders as its
// canonical "algo:hexdigest" string, an absent one as "". A field tagged
// `omitzero` never sees the absent case — the field is omitted instead.
func (r HashRef) MarshalJSON() ([]byte, error) {
	if r.h == nil {
		return []byte(`""`), nil
	}
	return json.Marshal(r.h.String())
}

// UnmarshalJSON implements json.Unmarshaler: "" and null mean absent, anything
// else must parse as a hash. A malformed or unknown-algorithm reference is
// rejected here (ErrInvalidHash / ErrUnknownAlgorithm), so an object read from
// a store can never hold an unparsable reference that later "disappears" from
// References().
func (r *HashRef) UnmarshalJSON(data []byte) error {
	var s *string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("cas: decode hash reference: %w", err)
	}
	if s == nil || *s == "" {
		r.h = nil
		return nil
	}
	h, err := ParseHash(*s)
	if err != nil {
		return err
	}
	r.h = h
	return nil
}
