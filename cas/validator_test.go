package cas_test

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// strictObj is an Object[T] that declares an invariant via Validate
// (cas.Validator): it must be named.
type strictObj struct {
	Name string `json:"name"`
}

func (strictObj) Type() string             { return "strict@1" }
func (strictObj) References() []cas.Digest { return nil }
func (o strictObj) Validate() error {
	if o.Name == "" {
		return errors.New("strict: name required")
	}
	return nil
}

// ptrObj is a pointer-receiver Object[T], used for the nil-object paths.
type ptrObj struct {
	Name string `json:"name"`
}

func (*ptrObj) Type() string             { return "ptr@1" }
func (*ptrObj) References() []cas.Digest { return nil }
func (o *ptrObj) Validate() error {
	if o.Name == "" {
		return errors.New("ptr: name required")
	}
	return nil
}

func newStrictStore(t *testing.T) (*cas.Store[strictObj], cas.Backend) {
	t.Helper()
	raw := mem.New()
	return cas.New(raw, jsoncodec.New[strictObj](), sha256.New()), raw
}

// storeRaw builds a TLV envelope by hand and writes it at the byte layer, so a
// test can plant a payload that Put would refuse (the same trick the other
// byte-layer tests use).
func storeRaw(t *testing.T, raw cas.Backend, typeName, payload string) cas.Digest {
	t.Helper()
	var buf []byte
	buf = append(buf, 1) // envelope version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf = append(buf, lenBuf[:n]...)
	buf = append(buf, typeName...)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf = append(buf, lenBuf[:n]...)
	buf = append(buf, payload...)
	d := sha256.Of(buf)
	if err := raw.Put(context.Background(), d, strings.NewReader(string(buf))); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestPutEnforcesValidate pins the write half of the Validator contract: an
// object that violates its own invariant is never written, and the object's own
// error is preserved in the chain.
func TestPutEnforcesValidate(t *testing.T) {
	ctx := context.Background()
	s, _ := newStrictStore(t)

	_, err := s.Put(ctx, strictObj{})
	if err == nil {
		t.Fatal("Put of an invalid object must fail")
	}
	if !strings.Contains(err.Error(), "name required") {
		t.Fatalf("Put error = %v, want the object's own error in the chain", err)
	}
	if _, _, err := s.PutDedup(ctx, strictObj{}); err == nil {
		t.Fatal("PutDedup of an invalid object must fail")
	}

	// A valid object stores and reads back.
	d, err := s.Put(ctx, strictObj{Name: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d)
	if err != nil || got.Name != "ok" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

// TestGetEnforcesValidateOnDecode pins the read half: an object that violates
// its own invariant — here planted at the byte layer, as a foreign tool might —
// is ErrCorrupt rather than handed back in an impossible state.
func TestGetEnforcesValidateOnDecode(t *testing.T) {
	ctx := context.Background()
	s, raw := newStrictStore(t)

	d := storeRaw(t, raw, "strict@1", `{"name":""}`)
	_, err := s.Get(ctx, d)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(invalid stored object) = %v, want ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "name required") {
		t.Fatalf("Get error = %v, want the object's own error in the chain", err)
	}

	// GetRaw does not decode, so it still returns the bytes: validation is a
	// typed-layer rule (an inspector must be able to read a broken object).
	if _, err := s.GetRaw(ctx, d); err != nil {
		t.Fatalf("GetRaw = %v, want the raw bytes", err)
	}
}

// TestPutRejectsNilObject pins that a nil object is refused instead of being
// encoded as a payload that decodes back to nil.
func TestPutRejectsNilObject(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[*ptrObj](), sha256.New())
	if _, err := s.Put(ctx, nil); err == nil {
		t.Fatal("Put(nil) must fail")
	}
	if _, _, err := s.PutDedup(ctx, nil); err == nil {
		t.Fatal("PutDedup(nil) must fail")
	}
}

// TestGetRejectsNilDecodedObject pins that a stored payload decoding to nil is
// corrupt: there is no object there to return.
func TestGetRejectsNilDecodedObject(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	s := cas.New(raw, jsoncodec.New[*ptrObj](), sha256.New())

	d := storeRaw(t, raw, "ptr@1", `null`)
	if _, err := s.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(payload null) = %v, want ErrCorrupt", err)
	}

	// The nil check precedes the type check, so a null payload under a foreign
	// type name is still ErrCorrupt — no method is called on the nil value.
	d = storeRaw(t, raw, "other@1", `null`)
	if _, err := s.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(payload null, type other@1) = %v, want ErrCorrupt", err)
	}
}

// TestValidatorIsOptional pins that a type without Validate is unaffected: the
// store stores and reads it with no extra rule.
func TestValidatorIsOptional(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Note](), sha256.New())
	d, err := s.Put(ctx, test.Note{})
	if err != nil {
		t.Fatalf("Put of an object without Validate = %v", err)
	}
	if _, err := s.Get(ctx, d); err != nil {
		t.Fatalf("Get = %v", err)
	}
}

// ifaceObj is an interface type that satisfies Object[ifaceObj]: T may itself be
// an interface, which is the case the reflect.Invalid guard in isNilValue
// exists for (a nil interface value has no kind to inspect).
type ifaceObj interface {
	cas.Object[ifaceObj]
}

// implIface is the concrete implementation the interface-typed store decodes to.
type implIface struct{ Name string }

func (implIface) Type() string             { return "iface@1" }
func (implIface) References() []cas.Digest { return nil }

// TestNilObjectWithInterfaceType pins that a nil object is rejected — not a
// panic — when T is an interface type, on both paths (Put before encoding, Get
// after decoding a payload that is literally null).
func TestNilObjectWithInterfaceType(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	s := cas.New[ifaceObj](raw, jsoncodec.New[ifaceObj](), sha256.New())

	err := error(nil)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Put(nil) with an interface T panicked: %v", r)
			}
		}()
		_, err = s.Put(ctx, nil)
	}()
	if err == nil || !strings.Contains(err.Error(), "nil object") {
		t.Fatalf("Put(nil) = %v, want a nil-object error", err)
	}
	if _, _, err := s.PutDedup(ctx, nil); err == nil {
		t.Fatal("PutDedup(nil) must fail")
	}

	d := storeRaw(t, raw, "iface@1", `null`)
	if _, err := s.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(payload null, interface T) = %v, want ErrCorrupt", err)
	}
}

// unversionedObj returns a type name without a major version. Object.Type MUST
// return "<type>@<major>"; writing an unversioned name would store "x@1" in the
// envelope (parseEnvelope's legacy rule) and then fail the decoded-type check on
// every read, i.e. a write-only object.
type unversionedObj struct{ Name string }

func (unversionedObj) Type() string             { return "unversioned" }
func (unversionedObj) References() []cas.Digest { return nil }

// TestPutRejectsUnversionedTypeName pins the write-side guard: an unversioned
// Type() is refused instead of producing an object Get can never read.
func TestPutRejectsUnversionedTypeName(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[unversionedObj](), sha256.New())
	if _, err := s.Put(ctx, unversionedObj{Name: "x"}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Put(unversioned type) = %v, want ErrUnknownType", err)
	}
	if _, _, err := s.PutDedup(ctx, unversionedObj{Name: "x"}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("PutDedup(unversioned type) = %v, want ErrUnknownType", err)
	}
}
