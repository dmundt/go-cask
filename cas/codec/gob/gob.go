// Package gob provides a Codec[T] for the cas core that serializes typed
// values with the standard library's encoding/gob (cas-core §4.6).
//
// This is an opt-in compatibility codec for Go-only workflows. It is not the
// recommended format for durable or cross-language CAS storage: both producer
// and consumer must be Go programs using the same type, and the format is
// neither a long-term archive format nor a stable external wire format.
//
// NewRaw[T]() returns a codec for any storable value T, so a store can be built
// directly: cas.New(raw, gob.NewRaw[T](), algo). It satisfies the codec
// round-trip contract, Decode(Encode(v)) == v.
//
// Decode recursion follows the DESTINATION type, not the payload. The decoder
// recurses as deep as the value of T it is handed, so a payload cannot invent
// nesting the caller's type does not already have; the one payload-only
// recursion path — skipping a field the destination does not know — is capped
// by the standard library's own nesting limit (10 000). A RECURSIVE destination
// type is the exception, and it is the caller's: a crafted, type-compatible
// chain of non-nil pointers drives the decoder one stack frame per level until
// the goroutine stack is exhausted — a fatal error no recover can catch — so a
// recursive T (a linked list or tree of pointers, `type Node struct{ Next
// *Node }`) MUST NOT be decoded from untrusted bytes (go-cask#453).
//
// No depth bound is imposed here, deliberately: bounding a decode whose depth
// the destination type defines would mean reimplementing encoding/gob's decoder
// (and reflection is not this package's to add). A caller that must decode
// attacker-influenced bytes into a recursive type bounds the payload itself —
// a size ceiling before decode, or a wire format with a depth bound of its own,
// such as cas/codec/cbor's MaxDepth — and a non-recursive T, which is every
// object model this repository stores, needs no such bound.
package gob

import (
	"bytes"
	"encoding/gob"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/codec/internal/bounded"
)

// Codec[T] serializes values with encoding/gob. If a wrapped codec is supplied,
// the value is serialized through that codec first and then gob-encoded as a
// transport layer. This keeps the shape of the object graph unchanged while
// allowing codec stacks to be composed transparently.
type Codec[T any] struct {
	next cas.Codec[T]
}

// New returns a gob codec that wraps next: the value is serialized by the inner
// codec and the resulting bytes are then gob-encoded as a transport layer, so
// gob becomes the outermost layer in a cascade. A nil next is the documented
// escape hatch for gob-encoding T directly; NewRaw is the same thing in one
// word and reads better.
func New[T any](next cas.Codec[T]) Codec[T] {
	return Codec[T]{next: next}
}

// NewRaw returns a gob codec that gob-encodes T directly, with no inner codec.
// Use it instead of New(nil) when nothing is wrapped.
func NewRaw[T any]() Codec[T] { return Codec[T]{} }

// Encode gob-encodes v directly or, when wrapped, the bytes produced by the
// inner codec.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next != nil {
		payload, err := c.next.Encode(v)
		if err != nil {
			return nil, err
		}
		buf := bytes.NewBuffer(make([]byte, 0, len(payload)+64))
		if err := gob.NewEncoder(buf).Encode(payload); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	buf := bytes.NewBuffer(make([]byte, 0, 64))
	if err := gob.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode gob-decodes data into a fresh T, or into the wrapped payload bytes
// when the codec is composed with an inner layer.
//
// Recursion during the decode follows T, so the caller's type — not data —
// bounds how deep the decoder descends; see the package doc for the one
// exception (a recursive T) and what the caller must do about it (go-cask#453).
func (c Codec[T]) Decode(data []byte) (T, error) {
	var zero T
	if c.next != nil {
		var payload []byte
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&payload); err != nil {
			return zero, err
		}
		return c.next.Decode(payload)
	}

	var v T
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}

// CodecName reports the codec identity tag written into the envelope: "gob" for
// a direct gob codec, and "gob+<inner tag>" when this codec is stacked over an
// inner one — the bytes are then gob-wrapped inner bytes, not a gob-encoded T,
// so the two cannot share a tag. A stacked codec whose inner codec declares no
// tag reports "" (unspecified), so nesting an unnamed codec never manufactures
// a tag that later reads as a mismatch. It satisfies cas.CodecNamer.
func (c Codec[T]) CodecName() string {
	if c.next == nil {
		return "gob"
	}
	return bounded.ComposeTag("gob", c.next)
}
