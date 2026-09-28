package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	casjson "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// previewStoredBytes reads one stored object whole, so a test inspects the frame
// the seeder wrote rather than the object it built in memory.
func previewStoredBytes(t *testing.T, ctx context.Context, backend cas.Backend, d cas.Digest) []byte {
	t.Helper()
	rc, err := backend.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestSeededFramesCarryTheCodecThatEncodedThem pins go-cask#333: a seeded frame's
// codec tag is the identity of the codec that produced its payload, and that
// codec decodes the payload into the document the ordinal describes. The seeder
// used to write a `preview` tag over a synthetic byte pattern no codec produced,
// so every surface that trusts the tag — the store's ErrCodecMismatch check, the
// census, the viewer's Codec column and its `-codec` filter — named a format
// nothing in the tree implemented.
//
// The assertions are ordered as the defect was: the tag is the codec's own name,
// so no literal can drift from it; the payload decodes through that codec; and
// the decoded document re-encodes to the payload the frame holds, byte for byte,
// so the bytes are the codec's own output and not merely bytes the codec
// tolerates. The tampered ordinal of every block is the control: it is seeded
// with damaged bytes, and the codec the tag names must refuse them.
func TestSeededFramesCarryTheCodecThatEncodedThem(t *testing.T) {
	ctx := context.Background()
	hasher := sha256.New()
	backend := backmem.New()
	const count = 8
	if _, _, err := seedPreview(ctx, backend, hasher, count); err != nil {
		t.Fatal(err)
	}
	// The shipped codec is the JSON codec, and that is what every seeded store
	// now reports. Pinned here so a codec swap is a deliberate change of this
	// test, the census fixtures and cli.md §2 together instead of a silent shift
	// of the value an operator reads.
	const wantTag = "json"
	if name := previewCodec().CodecName(); name != wantTag {
		t.Fatalf("previewCodec().CodecName() = %q, want the shipped JSON codec's %q", name, wantTag)
	}

	digests := make([]cas.Digest, 0, count)
	for ordinal := range count {
		object, err := previewObjectFor(ordinal, digests, hasher)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, object.digest)

		env, err := cas.EnvelopeFromBytes(previewStoredBytes(t, ctx, backend, object.digest))
		if err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
		if want := previewCodec().CodecName(); env.Codec != want {
			t.Fatalf("ordinal %d: frame codec tag = %q, want the codec's own %q (a producer marker is not a format, go-cask#333)", ordinal, env.Codec, want)
		}
		if want := previewObjectType(ordinal); env.Type != want {
			t.Fatalf("ordinal %d: frame type = %q, want %q", ordinal, env.Type, want)
		}

		doc, err := previewCodec().Decode(env.Data)
		if previewCorruptOrdinal(ordinal) {
			if err == nil {
				t.Fatalf("ordinal %d is seeded with tampered bytes, but the codec its tag names decoded them", ordinal)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ordinal %d: the tag %q names a codec that cannot decode the payload it framed: %v", ordinal, env.Codec, err)
		}
		if doc.Ordinal != ordinal || doc.Type != env.Type {
			t.Fatalf("ordinal %d: payload document = {ordinal %d, type %q}, want {%d, %q}", ordinal, doc.Ordinal, doc.Type, ordinal, env.Type)
		}
		if len(doc.Refs) != len(object.references) {
			t.Fatalf("ordinal %d: payload carries %d references, the graph gives it %d", ordinal, len(doc.Refs), len(object.references))
		}
		for i, reference := range doc.Refs {
			if !reference.Equal(object.references[i]) {
				t.Fatalf("ordinal %d: payload reference %d = %s, want %s", ordinal, i, reference, object.references[i])
			}
		}
		// Decode then Encode is the round trip the tag promises: the payload is
		// the codec's own output for this document, not a byte pattern that
		// happens to survive a decode.
		again, err := previewCodec().Encode(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, env.Data) {
			t.Fatalf("ordinal %d: re-encoding the payload's own document gives %d bytes, but the frame holds %d — the payload is not the codec's output", ordinal, len(again), len(env.Data))
		}
		if got, want := len(env.Data), previewObjectSize(ordinal); got < want {
			t.Fatalf("ordinal %d: payload = %d B, want at least the representative %d B (the padding field carries the rest of the size)", ordinal, got, want)
		}
	}
}

// TestSeededPayloadSizesKeepTheDocumentedSpread pins the size property the
// preview exists for (cli.md §2): the payloads still cycle the representative
// sizes from 48 KiB up to 1.2 MiB — the browser sorts and paginates on them —
// while the small classes stay at their table value. The padding field is what
// carries the size now, and it is derived from the codec's own skeleton, so this
// test is the check that deriving it did not flatten the spread.
func TestSeededPayloadSizesKeepTheDocumentedSpread(t *testing.T) {
	hasher := sha256.New()
	sizes := map[int]bool{}
	digests := make([]cas.Digest, 0, 12)
	for ordinal := range 12 {
		object, err := previewObjectFor(ordinal, digests, hasher)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, object.digest)
		env, err := cas.EnvelopeFromBytes(object.data)
		if err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
		sizes[len(env.Data)] = true
		if got, want := len(env.Data), previewObjectSize(ordinal); got < want {
			t.Fatalf("ordinal %d: payload = %d B, want at least the representative %d B", ordinal, got, want)
		}
	}
	// The three large classes sit far above the document's own skeleton, so they
	// are reached exactly; the small classes are a floor, because a document with
	// references at 128 B is larger than 128 B — the same floor the seeder's
	// max(payloadSize, len(header)) always was.
	for _, want := range []int{48 << 10, 384 << 10, 1200 << 10} {
		if !sizes[want] {
			t.Fatalf("no seeded payload is %d B: the size spread the browser sorts on collapsed (sizes %v)", want, sizes)
		}
	}
	if !sizes[128] {
		t.Fatalf("no seeded payload is the 128 B floor of the table (sizes %v)", sizes)
	}
}

// previewDocObject is a seeded document as a cas.Object, so a real Store built
// on the preview codec can read a seeded frame the way any consumer would. Its
// field names and order mirror previewDocument — the JSON keys are the wire
// contract — so a drift in the document shape fails this read instead of passing
// silently.
type previewDocObject struct {
	Ordinal int          `json:"ordinal"`
	Kind    string       `json:"type"`
	Refs    []cas.Digest `json:"refs,omitempty"`
	Padding string       `json:"padding"`
}

// Type reports the versioned type name the document carries.
func (o previewDocObject) Type() string { return o.Kind }

// References reports the digests the document carries.
func (o previewDocObject) References() []cas.Digest { return o.Refs }

// TestSeededPayloadReadsThroughAStoreBuiltOnItsCodec is the round trip the issue
// says the demo store could not show: a store built on the codec the seeded
// frames name reads a seeded object without a codec mismatch, hands back the
// document whose type the frame declares, and reports the tampered ordinal of
// each block as ErrCorrupt rather than decoding it.
func TestSeededPayloadReadsThroughAStoreBuiltOnItsCodec(t *testing.T) {
	ctx := context.Background()
	hasher := sha256.New()
	backend := backmem.New()
	const count = 8
	if _, _, err := seedPreview(ctx, backend, hasher, count); err != nil {
		t.Fatal(err)
	}
	// The store reads with the codec the seeder writes with: its tag is compared
	// against every frame's tag (cas.Store.checkCodec), so the same tag is the
	// only configuration in which a seeded object is readable at all.
	store := cas.New[previewDocObject](backend, casjson.New[previewDocObject](), hasher)

	digests := make([]cas.Digest, 0, count)
	for ordinal := range count {
		object, err := previewObjectFor(ordinal, digests, hasher)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, object.digest)

		doc, err := store.Get(ctx, object.digest)
		if previewCorruptOrdinal(ordinal) {
			if !errors.Is(err, cas.ErrCorrupt) {
				t.Fatalf("ordinal %d is seeded corrupt, but Get = (%+v, %v), want %v", ordinal, doc, err, cas.ErrCorrupt)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ordinal %d: Get = %v, want the seeded document — a frame the store's codec cannot decode is the go-cask#333 defect", ordinal, err)
		}
		if doc.Ordinal != ordinal || doc.Type() != previewObjectType(ordinal) {
			t.Fatalf("ordinal %d: Get = {ordinal %d, type %q}, want {%d, %q}", ordinal, doc.Ordinal, doc.Type(), ordinal, previewObjectType(ordinal))
		}
	}
}
