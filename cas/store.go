package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
)

// Store is the generic, type-safe content-addressable store for objects of
// type T, over a Backend backend, a Codec[T] and the client's Hasher. Type
// safety comes from one store per type: Store[Blob] and Store[Commit] are
// distinct, so passing a commit digest to a blob store is a compile-time error.
// Store is safe for concurrent use if its Backend is.
//
// Stored objects are self-describing: the codec payload is wrapped in the TLV
// envelope
// [version u8][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload]
// (envelope.go, cas-core §8 decision 1), so the versioned type name (e.g.
// "commit@1") and the codec identity tag travel with the bytes without a side
// registry. The digest covers the whole envelope, so the type is part of the
// address. The type name and the frame's format version are each readable on
// their own — Type and Version — so neither costs a payload read.
//
// The typed layer is constrained: T MUST implement Object[T]. The type
// system therefore proves that every value a Store handles is an object —
// Store[plain] does not compile, Put takes the concrete T, and no runtime
// type assertions exist anywhere in the typed layer.
//
// Store.Get reads one object per call. Loading many objects — a whole revision,
// a traversal — is a caching problem rather than a store method: warm a cache
// with cachemem.CachedStore[T] (cas/cache/mem), lru.Cache[T] (cas/cache/lru) or
// prefetch.SmartCache[T] (cas/cache/prefetch), size it from the backend's
// Stats, and read through it; at the raw byte layer the package-level GetMany
// (batch.go) does the same for a batch of digests in one call. cas-core §4.13
// records the prefetch recipe.
//
// Closing is the store's only lifecycle step: Close is idempotent and forwards
// to a backend that implements io.Closer, so when the backend over which one or
// more stores are built is a closer (a backend that flushes on close, such as
// cas/backend/packfs), call Close on the store — or the backend — once the
// stores over it are finished. See Close.
type Store[T Object[T]] struct {
	backend   Backend
	codec     Codec[T]
	codecName string
	hasher    Hasher
	closeFn   func() error
}

// New creates a Store[T] over backend with codec, hashing through hasher. It
// cannot fail: the core resolves nothing and knows no algorithm (cas-core §4.2),
// and it names no codec either — the client supplies one, which is what keeps
// `cas` free of any dependency on a `cas/codec` subpackage (library-design §1).
// There is deliberately no NewJSON/NewCompressedJSON in the core for the same
// reason; a consumer that repeats the construction writes its own one-line
// constructor — func newNoteStore(backend Backend, hasher Hasher) *Store[*Note]
// { return New(backend, json.New[*Note](), hasher) } — and a consumer that has
// several types can register each store once with cas/repo.RegisterStore and
// read it back typed with cas/repo.LookupStore[T].
//
// When codec implements CodecNamer its identity tag is resolved once, here: the
// tag is written into every envelope this store produces and compared on every
// Get, so reading an object written with another codec is reported as
// ErrCodecMismatch rather than as a decode failure. A codec that declares no tag
// writes an empty tag and reads any tag without complaint.
func New[T Object[T]](backend Backend, codec Codec[T], hasher Hasher) *Store[T] {
	s := &Store[T]{backend: backend, codec: codec, codecName: codecNameOf(codec), hasher: hasher}
	// The close result is memoised, so the backend is closed exactly once and
	// every caller observes the first error (sync.OnceValue, not OnceFunc).
	s.closeFn = sync.OnceValue(func() error {
		if closer, ok := backend.(io.Closer); ok {
			return closer.Close()
		}
		return nil
	})
	return s
}

// codecNameOf resolves a codec's optional identity tag (CodecNamer): a codec
// that does not implement the interface declares no tag. The assertion runs on
// the codec value the caller already supplied — no reflection, no any — and its
// answer is fixed for the store's lifetime, so the tag is resolved once at
// construction rather than on every read.
func codecNameOf[T any](codec Codec[T]) string {
	if namer, ok := codec.(CodecNamer); ok {
		return namer.CodecName()
	}
	return ""
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

// encoded produces the stored form of obj together with its digest: the
// validation, codec encode, envelope framing, hashing and key check that every
// write path shares, so Put and PutDedup cannot drift apart. obj is validated
// before anything is encoded, so an invalid object is never written.
func (s *Store[T]) encoded(obj T) (Digest, []byte, error) {
	if err := validateObject(obj); err != nil {
		return nil, nil, err
	}
	data, err := s.marshal(obj)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.hasher.Digest(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	if err := s.check(d, "store: put"); err != nil {
		return nil, nil, err
	}
	return d, data, nil
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
	d, data, err := s.encoded(obj)
	if err != nil {
		return nil, err
	}
	if err := s.backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return d, nil
}

// Close releases any backend resources if the backend implements io.Closer.
// Call it once the stores over a closer backend are finished — that backend
// close is the flush, for example packfs's pack index, so a store left open
// leaves unsaved state behind. The call is idempotent: the backend's Close runs
// exactly once, a second call is a no-op, and both return that first call's
// error. A backend that needs no cleanup makes it a no-op, so defer
// store.Close() is always safe.
func (s *Store[T]) Close() error {
	if s == nil || s.closeFn == nil {
		return nil
	}
	return s.closeFn()
}

// PutDedup is Put that first checks whether the content already exists; it
// returns (d, true, nil) when the object was already stored (deduplicated)
// and (d, false, nil) when it was written now.
func (s *Store[T]) PutDedup(ctx context.Context, obj T) (Digest, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	d, data, err := s.encoded(obj)
	if err != nil {
		return nil, false, err
	}
	exists, err := s.backend.Exists(ctx, d)
	if err != nil {
		return nil, false, err
	}
	if exists {
		return d, true, nil
	}
	if err := s.backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return nil, false, err
	}
	return d, false, nil
}

// marshal builds the stored form of obj: the TLV envelope
// [version][codecLen][codec][typeLen][type][payloadLen][codec payload] (see
// envelope.go). The codec is the single serialization authority — the same
// codec decodes on read (Get) — and its identity tag (s.codecName, resolved
// from CodecNamer at construction) is written into the header. obj is the
// concrete T (the Store constraint), so no type assertion is involved.
func (s *Store[T]) marshal(obj T) ([]byte, error) {
	payload, err := s.codec.Encode(obj)
	if err != nil {
		return nil, fmt.Errorf("cas: encode: %w", err)
	}
	typ := obj.Type()
	if typ == "" {
		// An empty type name produces an envelope that decodeEnvelope rejects,
		// i.e. an object Put succeeds on but Get can never read.
		return nil, fmt.Errorf("%w: empty type name", ErrUnknownType)
	}
	if !strings.Contains(typ, "@") {
		// Object[T].Type MUST return a versioned name "<type>@<major>"
		// (object.go, object-versioning.md). decodeEnvelope reads a legacy
		// unversioned name as "@1", so writing one produces an object whose
		// stored type ("legacy@1") can never equal the decoded Type()
		// ("legacy"): a write-only object. Reject it at the source instead.
		return nil, fmt.Errorf("%w: type name %q is not versioned (want \"<type>@<major>\")", ErrUnknownType, typ)
	}
	return encodeEnvelope(s.codecName, typ, payload), nil
}

// Get reads the object at d and returns the concrete T directly — no casts.
// It decodes the TLV envelope, compares the stored codec identity tag with the
// store's own (ErrCodecMismatch when both are present and differ), decodes the
// payload with the store's codec, and checks the decoded type matches the
// stored type (a self-describing store refuses to hand back a value of the
// wrong type).
//
// The errors separate "damaged bytes" from "not my type". A stored envelope
// that does not parse is ErrCorrupt, naming the offending field — the same
// answer Store.Type and PeekType give for the same header — as is a payload the
// codec cannot decode, one that decodes to nil, and one that violates its
// object's Validate. ErrUnknownType means the envelope is intact and names a
// type this store does not decode; it is never a parse failure.
func (s *Store[T]) Get(ctx context.Context, d Digest) (T, error) {
	var zero T
	data, err := s.GetRaw(ctx, d)
	if err != nil {
		return zero, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return zero, err
	}
	if err := s.checkCodec(env); err != nil {
		return zero, err
	}
	v, err := s.codec.Decode(env.Data)
	if err != nil {
		if env.Codec == "" {
			// The object carries no codec identity (a v1 envelope, or one
			// written by a codec without CodecNamer), so a difference cannot be
			// proven: the bytes are indistinguishable from damage and ErrCorrupt
			// stays the answer. Naming the absent identity puts the diagnosis
			// one step from ErrCodecMismatch.
			return zero, fmt.Errorf("cas: %w: %s: object carries no codec identity: %w", ErrCorrupt, env.Type, err)
		}
		return zero, fmt.Errorf("cas: %w: payload decode: %w", ErrCorrupt, err)
	}
	// A payload that decodes to nil (e.g. JSON `null`) is not an object: reject
	// it before calling any method on it.
	if isNilValue(v) {
		return zero, fmt.Errorf("%w: %s: decoded to nil", ErrCorrupt, env.Type)
	}
	if v.Type() != env.Type {
		return zero, fmt.Errorf("%w: stored type %q != decoded type %q", ErrUnknownType, env.Type, v.Type())
	}
	if err := validateDecoded(v, env.Type); err != nil {
		return zero, err
	}
	return v, nil
}

// checkCodec compares the codec identity tag recorded in env with the tag of
// the codec this store reads with, and reports a difference as
// ErrCodecMismatch. The check applies only when both sides declare a tag: an
// object with none (a version 1 envelope, or one written by a codec without
// CodecNamer) and a store whose codec declares none are read exactly as before,
// which is the documented compatibility rule. It runs after the header is
// parsed and before the payload is decoded, so a codec difference never
// surfaces as a decode failure.
func (s *Store[T]) checkCodec(env Envelope) error {
	if s.codecName == "" || env.Codec == "" || env.Codec == s.codecName {
		return nil
	}
	return fmt.Errorf("%w: %s: object was written with codec %q, this store reads %q",
		ErrCodecMismatch, env.Type, env.Codec, s.codecName)
}

// GetRaw returns the raw stored bytes — the self-describing TLV envelope —
// for inspection and tooling. It buffers the whole object.
//
// It does not parse the envelope, so it reports no envelope-level error: a
// damaged frame comes back as its bytes, and a caller that wants the verdict
// parses them with EnvelopeFromBytes (or reads the object with Get, which
// parses the same bytes and reports ErrCorrupt). Only the guards and the
// backend's own failures — ErrInvalidDigest, ErrNotFound — surface here.
func (s *Store[T]) GetRaw(ctx context.Context, d Digest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.check(d, "store: get"); err != nil {
		return nil, err
	}
	rc, err := s.backend.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("cas: read object: %w", err)
	}
	if err := rc.Close(); err != nil {
		return nil, fmt.Errorf("cas: close object: %w", err)
	}
	return data, nil
}

// Type reports the versioned type name stored at d without decoding the payload
// or reading it. It opens the object, reads the envelope header (PeekType) and
// closes the reader, so a large object costs a header read rather than a full
// read and allocation. That is what makes "enumerate the store by type" a List
// followed by one Type per digest, where Get would decode every payload.
//
// It reports the type as stored, which is not necessarily the type this store
// decodes: a digest holding a different type or major version is reported here
// and rejected by Get. An absent object returns the backend's ErrNotFound, and
// an unusable header returns ErrCorrupt with the offending field named.
func (s *Store[T]) Type(ctx context.Context, d Digest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.check(d, "store: type"); err != nil {
		return "", err
	}
	rc, err := s.backend.Get(ctx, d)
	if err != nil {
		return "", err
	}
	typeName, err := PeekType(rc)
	if err != nil {
		_ = rc.Close() // the header error is the one worth reporting
		return "", err
	}
	if err := rc.Close(); err != nil {
		return "", fmt.Errorf("cas: close object: %w", err)
	}
	return typeName, nil
}

// Version reports the envelope format version stored at d without decoding the
// payload or reading it. It opens the object, reads the frame's leading byte
// (PeekVersion) and closes the reader, so a large object costs a single-byte
// read rather than a full read and allocation.
//
// It reports the version as stored, whether or not this build knows it: a store
// legitimately holds frames of more than one envelope layout, and the version
// byte is the only thing that says which. A caller compares it against
// EnvelopeVersion to tell "written by a newer format" from "corrupt bytes"
// without matching an error message. An absent object returns the backend's
// ErrNotFound, and a stream with no byte at all returns ErrCorrupt naming the
// field.
func (s *Store[T]) Version(ctx context.Context, d Digest) (byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := s.check(d, "store: version"); err != nil {
		return 0, err
	}
	rc, err := s.backend.Get(ctx, d)
	if err != nil {
		return 0, err
	}
	version, err := PeekVersion(rc)
	if err != nil {
		_ = rc.Close() // the header error is the one worth reporting
		return 0, err
	}
	if err := rc.Close(); err != nil {
		return 0, fmt.Errorf("cas: close object: %w", err)
	}
	return version, nil
}

// Exists reports whether the object is stored. Delegates to the backend.
func (s *Store[T]) Exists(ctx context.Context, d Digest) (bool, error) {
	if err := s.check(d, "store: exists"); err != nil {
		return false, err
	}
	return s.backend.Exists(ctx, d)
}

// Delete removes the object. A missing object is a no-op. Delegates to the
// backend.
func (s *Store[T]) Delete(ctx context.Context, d Digest) error {
	if err := s.check(d, "store: delete"); err != nil {
		return err
	}
	return s.backend.Delete(ctx, d)
}
