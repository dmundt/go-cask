package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Store[T] is the generic, type-safe content-addressable store for objects of
// type T, over a Backend backend, a Codec[T] and the client's Hasher. Type
// safety comes from one store per type: Store[Blob] and Store[Commit] are
// distinct, so passing a commit digest to a blob store is a compile-time error.
// Store[T] is safe for concurrent use if its Backend is.
//
// Stored objects are self-describing: the codec payload is wrapped in the TLV
// envelope [version u8][uvarint typeLen][type][uvarint payloadLen][payload]
// (envelope.go, cas-core §8 decision 1), so the versioned type name (e.g.
// "commit@1") travels with the bytes without a side registry. The digest covers
// the whole envelope, so the type is part of the address.
//
// The typed layer is constrained: T MUST implement Object[T]. The type
// system therefore proves that every value a Store handles is an object —
// Store[plain] does not compile, Put takes the concrete T, and no runtime
// type assertions exist anywhere in the typed layer.
type Store[T Object[T]] struct {
	raw    Backend
	codec  Codec[T]
	hasher Hasher
}

// New creates a Store[T] over raw with codec, hashing through hasher. It cannot
// fail: the core resolves nothing and knows no algorithm (cas-core §4.2).
func New[T Object[T]](raw Backend, codec Codec[T], hasher Hasher) *Store[T] {
	return &Store[T]{raw: raw, codec: codec, hasher: hasher}
}

// check applies the guards every store operation shares: the digest must be
// present (CheckDigest) and well formed for the client's algorithm.
func (s *Store[T]) check(d Digest, what string) error {
	if err := CheckDigest(d, what); err != nil {
		return err
	}
	if err := s.hasher.Validate(d); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// validateObject applies T's own invariant check, when it declares one, before
// anything is encoded or written (Validator). A nil object is rejected here:
// there is nothing to store, and a nil would otherwise encode as a payload that
// decodes back to nil.
func validateObject[T any](obj T) error {
	if isNilValue(obj) {
		return errors.New("cas: put: nil object")
	}
	if v, ok := any(obj).(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("cas: put: %w", err)
		}
	}
	return nil
}

// validateDecoded applies T's own invariant check, when it declares one, to a
// value a codec produced: a stored object that violates its own invariants is
// ErrCorrupt rather than handed back in an impossible state. The caller checks
// for nil first, so this never calls Validate on an absent value.
func validateDecoded[T any](obj T, typeName string) error {
	if v, ok := any(obj).(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrCorrupt, typeName, err)
		}
	}
	return nil
}

// isNilValue reports whether v carries no value: a nil interface value itself,
// or a nil pointer, map, slice, channel, function or interface. It is the
// core's only use of reflection, and it decides nothing but "there is no value
// here" — the callers above need it so that a nil object is rejected instead of
// panicking inside the object's own Type or Validate.
func isNilValue[T any](v T) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return true // v is a nil interface value: reflect has no kind to inspect
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// Put encodes obj with the store codec, prepends the type string, and
// stores it. The digest covers the type AND the payload, so identical content
// always produces the identical address (dedup) and a type change produces
// a new address. When T declares Validate() (Validator) it runs first, so an
// invalid object is never written. The bytes are hashed in a single pass and
// streamed to the backend without buffering (performance §3).
func (s *Store[T]) Put(ctx context.Context, obj T) (Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateObject(obj); err != nil {
		return nil, err
	}
	data, err := s.marshal(obj)
	if err != nil {
		return nil, err
	}
	d, err := s.hasher.Digest(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if err := s.check(d, "store: put"); err != nil {
		return nil, err
	}
	if err := s.raw.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return d, nil
}

// PutDedup is Put that first checks whether the content already exists; it
// returns (d, true, nil) when the object was already stored (deduplicated)
// and (d, false, nil) when it was written now.
func (s *Store[T]) PutDedup(ctx context.Context, obj T) (Digest, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validateObject(obj); err != nil {
		return nil, false, err
	}
	data, err := s.marshal(obj)
	if err != nil {
		return nil, false, err
	}
	d, err := s.hasher.Digest(bytes.NewReader(data))
	if err != nil {
		return nil, false, err
	}
	if err := s.check(d, "store: put"); err != nil {
		return nil, false, err
	}
	exists, err := s.raw.Exists(ctx, d)
	if err != nil {
		return nil, false, err
	}
	if exists {
		return d, true, nil
	}
	if err := s.raw.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return nil, false, err
	}
	return d, false, nil
}

// marshal builds the stored form of obj: the TLV envelope
// [version][typeLen][type][codec payload] (see envelope.go). The codec is the
// single serialization authority — the same codec decodes on read (Get). obj
// is the concrete T (the Store constraint), so no type assertion is involved.
func (s *Store[T]) marshal(obj T) ([]byte, error) {
	payload, err := s.codec.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("cas: encode: %w", err)
	}
	typ := obj.Type()
	if typ == "" {
		// An empty type name produces an envelope that parseEnvelope rejects,
		// i.e. an object Put succeeds on but Get can never read.
		return nil, fmt.Errorf("%w: empty type name", ErrUnknownType)
	}
	if !strings.Contains(typ, "@") {
		// Object[T].Type MUST return a versioned name "<type>@<major>"
		// (object.go, object-versioning.md). parseEnvelope reads a legacy
		// unversioned name as "@1", so writing one produces an object whose
		// stored type ("legacy@1") can never equal the decoded Type()
		// ("legacy"): a write-only object. Reject it at the source instead.
		return nil, fmt.Errorf("%w: type name %q is not versioned (want \"<type>@<major>\")", ErrUnknownType, typ)
	}
	return marshalEnvelope(typ, payload), nil
}

// Get reads the object at d and returns the concrete T directly — no casts.
// It decodes the TLV envelope, decodes the payload with the store's codec, and
// checks the decoded type matches the stored type (a self-describing store
// refuses to hand back a value of the wrong type).
func (s *Store[T]) Get(ctx context.Context, d Digest) (T, error) {
	var zero T
	data, err := s.GetRaw(ctx, d)
	if err != nil {
		return zero, err
	}
	typeName, payload, err := parseEnvelope(data)
	if err != nil {
		return zero, err
	}
	v, err := s.codec.Unmarshal(payload)
	if err != nil {
		return zero, fmt.Errorf("cas: %w: payload decode: %w", ErrCorrupt, err)
	}
	// A payload that decodes to nil (e.g. JSON `null`) is not an object: reject
	// it before calling any method on it.
	if isNilValue(v) {
		return zero, fmt.Errorf("%w: %s: decoded to nil", ErrCorrupt, typeName)
	}
	if v.Type() != typeName {
		return zero, fmt.Errorf("%w: stored type %q != decoded type %q", ErrUnknownType, typeName, v.Type())
	}
	if err := validateDecoded(v, typeName); err != nil {
		return zero, err
	}
	return v, nil
}

// GetRaw returns the raw stored bytes — the self-describing TLV envelope —
// for inspection and tooling. It buffers the whole object.
func (s *Store[T]) GetRaw(ctx context.Context, d Digest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.check(d, "store: get"); err != nil {
		return nil, err
	}
	rc, err := s.raw.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("cas: read object: %w", err)
	}
	return data, nil
}

// Exists reports whether the object is stored. Delegates to the backend.
func (s *Store[T]) Exists(ctx context.Context, d Digest) (bool, error) {
	if err := s.check(d, "store: exists"); err != nil {
		return false, err
	}
	return s.raw.Exists(ctx, d)
}

// Delete removes the object. A missing object is a no-op. Delegates to the
// backend.
func (s *Store[T]) Delete(ctx context.Context, d Digest) error {
	if err := s.check(d, "store: delete"); err != nil {
		return err
	}
	return s.raw.Delete(ctx, d)
}
