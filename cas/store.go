package cas

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// Put encodes obj with the store codec, prepends the type string, and
// stores it. The digest covers the type AND the payload, so identical content
// always produces the identical address (dedup) and a type change produces
// a new address. The bytes are hashed in a single pass and streamed to the
// backend without buffering (performance §3).
func (s *Store[T]) Put(ctx context.Context, obj T) (Digest, error) {
	if err := ctx.Err(); err != nil {
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
	if v.Type() != typeName {
		return zero, fmt.Errorf("%w: stored type %q != decoded type %q", ErrUnknownType, typeName, v.Type())
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
