package cas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Store[T] is the generic, type-safe content-addressed store for objects of
// type T, over a RawStore backend, a Codec[T] and a hash algorithm. Type
// safety comes from one store per type: Store[Blob] and Store[Commit] are
// distinct, so passing a commit hash to a blob store is a compile-time
// error. Store[T] is safe for concurrent use if its RawStore is.
//
// Stored objects use the self-describing envelope (cas-core §8, decision 1):
// the serialized bytes are {"type": "<type>@<major>", "data": "<base64
// payload>"}, where the payload is the codec output. Object[T].Serialize
// produces these envelope bytes (the object knows its own Type); Put hashes
// and stores them as-is. Get strips the envelope and decodes the payload
// with the store's codec. The base64 payload encoding keeps the envelope
// valid JSON for any codec output (JSON, gzip, binary).
type Store[T any] struct {
	raw    RawStore
	codec  Codec[T]
	hasher HashFunc
}

// NewStore creates a Store[T] over raw, resolving the hash algorithm from
// the registry at construction (no global dependence in the hot path). It
// returns ErrUnknownAlgorithm if algo is not registered. Custom algorithms
// are registered with RegisterHash before calling NewStore (cas-core §4.2).
func NewStore[T any](raw RawStore, codec Codec[T], algo string) (*Store[T], error) {
	fn, ok := lookupHash(algo)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algo)
	}
	return &Store[T]{raw: raw, codec: codec, hasher: fn}, nil
}

// Put serializes obj (its self-describing envelope bytes), computes the
// content address and stores them. The hash covers the type name AND the
// payload, so identical content always produces the identical address
// (dedup) and a type change produces a new address. The serialized bytes are
// hashed in a single pass and streamed to the backend without buffering
// (performance §3).
func (s *Store[T]) Put(ctx context.Context, obj Object[T]) (Hash, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := obj.Serialize()
	if err != nil {
		return nil, fmt.Errorf("cas: serialize: %w", err)
	}
	h := s.hasher(data)
	if err := s.raw.Put(ctx, h, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return h, nil
}

// PutDedup is Put that first checks whether the content already exists; it
// returns (h, true, nil) when the object was already stored (deduplicated)
// and (h, false, nil) when it was written now.
func (s *Store[T]) PutDedup(ctx context.Context, obj Object[T]) (Hash, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, err := obj.Serialize()
	if err != nil {
		return nil, false, fmt.Errorf("cas: serialize: %w", err)
	}
	h := s.hasher(data)
	exists, err := s.raw.Exists(ctx, h)
	if err != nil {
		return nil, false, err
	}
	if exists {
		return h, true, nil
	}
	if err := s.raw.Put(ctx, h, bytes.NewReader(data)); err != nil {
		return nil, false, err
	}
	return h, false, nil
}

// Get reads the object at h and returns it as Object[T]. This is the single
// documented use of a type assertion on T in the exported API; every other
// access path is fully typed. A missing object returns ErrNotFound; a value
// that decodes but does not implement Object[T] returns ErrUnknownType.
func (s *Store[T]) Get(ctx context.Context, h Hash) (Object[T], error) {
	v, err := s.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	obj, ok := any(v).(Object[T])
	if !ok {
		return nil, fmt.Errorf("%w: decoded value is not an Object[%T]", ErrUnknownType, v)
	}
	return obj, nil
}

// GetTyped reads the object at h and returns the concrete T directly — no
// casts. It strips the envelope and decodes the payload with the store's
// codec; it may buffer the object because Codec.Decode needs the full
// payload bytes. When the decoded value implements Object[T], its Type()
// must match the envelope's type name, otherwise ErrUnknownType is returned
// (a self-describing store refuses to hand back a value of the wrong type).
func (s *Store[T]) GetTyped(ctx context.Context, h Hash) (T, error) {
	var zero T
	data, err := s.GetRaw(ctx, h)
	if err != nil {
		return zero, err
	}
	envType, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return zero, err
	}
	v, err := s.codec.Decode(payload)
	if err != nil {
		return zero, err
	}
	if obj, ok := any(v).(Object[T]); ok && obj.Type() != envType {
		return zero, fmt.Errorf("%w: envelope type %q != decoded type %q", ErrUnknownType, envType, obj.Type())
	}
	return v, nil
}

// GetRaw returns the raw stored bytes — the self-describing envelope — for
// inspection and tooling. It buffers the whole object.
func (s *Store[T]) GetRaw(ctx context.Context, h Hash) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rc, err := s.raw.Get(ctx, h)
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
func (s *Store[T]) Exists(ctx context.Context, h Hash) (bool, error) {
	return s.raw.Exists(ctx, h)
}

// Delete removes the object. A missing object is a no-op. Delegates to the
// backend.
func (s *Store[T]) Delete(ctx context.Context, h Hash) error {
	return s.raw.Delete(ctx, h)
}

// envelope is the self-describing storage form (cas-core §8 decision 1):
// {"type": "<type>@<major>", "data": "<base64 codec payload>"}.
type envelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// unmarshalEnvelope parses the envelope, returning the versioned type name
// (an absent major version reads as "@1", object-versioning §2) and the
// base64-decoded payload.
func unmarshalEnvelope(data []byte) (string, []byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", nil, fmt.Errorf("%w: not a valid object envelope", ErrUnknownType)
	}
	if env.Type == "" {
		return "", nil, fmt.Errorf("%w: envelope missing type", ErrUnknownType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", nil, fmt.Errorf("%w: envelope data is not base64", ErrUnknownType)
	}
	typeName := env.Type
	if !strings.Contains(typeName, "@") {
		typeName += "@1" // legacy unversioned type name
	}
	return typeName, payload, nil
}
