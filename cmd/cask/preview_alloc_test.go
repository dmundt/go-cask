package main

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// presentBackend answers every existence probe with "stored", so the preview
// walk runs to its documented bound (maxPreviewCount) instead of stopping at the
// first absent block. previewReferences takes the minimal Backend contract and
// asks it exactly one question — Exists — so a probe that answers yes is the
// whole of what the walk needs to visit every ordinal.
type presentBackend struct{ cas.Backend }

// Exists reports every digest as stored.
func (presentBackend) Exists(context.Context, cas.Digest) (bool, error) { return true, nil }

// TestPreviewWalkAllocationFloorPerOrdinal pins the allocation volume of the
// viewer's startup walk (`cask web` calls previewReferences on every start,
// web.go), against a ceiling expressed in payloads per ordinal.
//
// A seeded payload is now the preview codec's own output — the frame's codec tag
// names the format that produced its bytes, so the payload can no longer be
// hand-filled into one buffer (go-cask#333) — and encoding/json materializes a
// payload twice: once in the encoder state it grows and re-grows (the collector
// drops that pool between ordinals), and once as the slice Marshal returns. The
// frame cas.EncodeEnvelope returns is the third materialization, and the last one
// the walk cannot avoid, so the floor moves from the 1.02 payloads the synthetic
// payload cost (go-cask#374) to ~3.0.
//
// What stayed avoidable, and what this test still guards, is the padding: one
// fill string the builder grows once and slices per ordinal. Building it per
// ordinal would add a payload-sized string for every ordinal on top of the
// codec's two and the frame — ~4.0 payloads, which this budget fails by
// construction. TestPreviewBuilderSlicesOneFill pins that mechanism
// deterministically, without a volume sample.
//
// The ceiling is 17/4 payloads (4.25). Measured on go1.27.1 windows/amd64 and
// linux/amd64 (WSL) over the walk's documented bound of 10000 ordinals:
// 8 431 660 008 B total, 843 166 B/ordinal, 3.02 payloads of 278 973 B — the same
// figure on both platforms, stable to hundredths of a payload across runs. The
// gate's race suite measures 3.88 payloads on the same tree: the race detector's
// own shadow allocations ride along with every payload the codec materializes, so
// the ceiling has to clear the instrumented figure while still failing a further
// payload-sized copy per ordinal. The benchmark suite is manual and CI runs no
// -bench, so this test is the guard that keeps the volume from creeping past the
// codec's own cost.
func TestPreviewWalkAllocationFloorPerOrdinal(t *testing.T) {
	var totalPayload int
	for ordinal := range maxPreviewCount {
		totalPayload += previewObjectSize(ordinal)
	}
	meanPayload := totalPayload / maxPreviewCount
	budget := int64(meanPayload) * 17 / 4

	// The collector stays on: TotalAlloc counts every byte allocated whether or
	// not it was collected, so the figure is the walk's own allocation volume and
	// the sample never has to hold the frames it discards.
	var before, after runtime.MemStats
	runtime.GC() // settle the test's own allocations before the sample opens
	runtime.ReadMemStats(&before)
	index, err := previewReferences(context.Background(), presentBackend{}, sha256.New())
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.outbound) != maxPreviewCount {
		t.Fatalf("the walk indexed %d of %d ordinals: the sample must visit every one", len(index.outbound), maxPreviewCount)
	}

	perOrdinal := int64(after.TotalAlloc-before.TotalAlloc) / maxPreviewCount
	t.Logf("preview walk over %d ordinals: %d B total, %d B/ordinal (%.2f payloads of %d B; ceiling %d B/ordinal)",
		maxPreviewCount, after.TotalAlloc-before.TotalAlloc, perOrdinal,
		float64(perOrdinal)/float64(meanPayload), meanPayload, budget)
	if perOrdinal > budget {
		t.Fatalf("the preview walk allocated %d B/ordinal, want <= %d B/ordinal = 4.25 payloads: the codec's payload (materialized twice by encoding/json) and the frame are the floor, so a further payload-sized allocation per ordinal means the document's padding fill is being rebuilt for every ordinal instead of sliced from one (go-cask#374, go-cask#333)",
			perOrdinal, budget)
	}
}

// TestPreviewBuilderSlicesOneFill pins the mechanism the volume budget rests on:
// once the fill has grown to the largest payload of a run, padding an ordinal
// allocates nothing, whatever size it asks for. It asserts allocations rather
// than volumes, so it holds on any machine and any collector setting where the
// volume sample above is only a budget.
func TestPreviewBuilderSlicesOneFill(t *testing.T) {
	largest := 0
	for ordinal := range maxPreviewCount {
		largest = max(largest, previewObjectSize(ordinal))
	}
	var builder previewBuilder
	builder.paddingFill(largest) // pay for the fill once, as a run of ordinals does

	var got string
	for _, size := range []int{0, 1, previewObjectSize(3), previewObjectSize(5), largest} {
		allocs := testing.AllocsPerRun(10, func() { got = builder.paddingFill(size) })
		if len(got) != size {
			t.Fatalf("paddingFill(%d) returned %d bytes, want %d", size, len(got), size)
		}
		if allocs != 0 {
			t.Fatalf("paddingFill(%d) allocated %.1f times, want 0: a run must slice one fill, not build a padding string per ordinal (go-cask#374)", size, allocs)
		}
	}
}

// previewEnvelopeByCodec frames one preview object the way the walk did before
// go-cask#374: a payload the codec produces for the ordinal, padded from a string
// allocated for that ordinal alone, and then copied whole into the frame
// cas.EncodeEnvelope returns. It is deliberately the allocating form — the
// reference the fill-reusing builder must reproduce byte for byte, since a
// preview object's digest is the address the store and the viewer must agree on
// (cli.md §2).
func previewEnvelopeByCodec(typ string, ordinal, payloadSize int, references []cas.Digest) ([]byte, error) {
	doc := previewDocument{Ordinal: ordinal, Type: typ, Refs: references}
	padding, err := previewPaddingLen(doc, payloadSize)
	if err != nil {
		return nil, err
	}
	doc.Padding = strings.Repeat(previewPaddingText, padding)
	payload, err := previewCodec().Encode(doc)
	if err != nil {
		return nil, err
	}
	return cas.EncodeEnvelope(previewCodec().CodecName(), typ, payload)
}

// TestPreviewBuilderReproducesTheCodecFraming proves the reused padding fill is a
// pure allocation fix (go-cask#374): for a sequence of ordinals that covers every
// size in the table, a byte that wraps past 255, and a large payload followed by
// the smallest one — the case a stale fill would corrupt — the builder's frame
// and digest are byte-identical to the per-ordinal encoding the codec does, and
// to the digests that encoding produced.
func TestPreviewBuilderReproducesTheCodecFraming(t *testing.T) {
	// A fixed reference list, so every object carries the references its ordinal
	// asks for (previewObjectReferences) whatever the digest chain would have
	// handed it.
	references := make([]cas.Digest, 0, 16)
	for i := range 16 {
		references = append(references, sha256.Of(fmt.Appendf(nil, "preview reference %d", i)))
	}
	// Ascending ordinals cycle the sizes 128 B → 1.2 MiB and shrink again, so the
	// fill is grown and reused; 0 after 9995 is the shrink case (1.2 MiB fill,
	// 128 B payload), and 1000/1001 cover ordinals whose byte() is not their own.
	ordinals := make([]int, 0, 28)
	for ordinal := range 24 {
		ordinals = append(ordinals, ordinal)
	}
	ordinals = append(ordinals, 1000, 1001, 9995, 9999, 0)

	// The anchor: ordinal 0 carries no references whatever the digest chain is
	// (previewObjectReferences asks for min(len(digests), 0)), so its address is a
	// fixed constant — the one every seeded preview store holds for the first
	// object, and the one the codec's own encoding produced. It is short enough to
	// check by hand against the TLV layout cas-core §8 d1 documents (a 143-byte
	// frame: 1 version + 1 codec length + 4 `json` + 1 type length + 6 `blob@1` +
	// 2 payload length + a 128-byte payload).
	const firstDigest = "sha256:909796aa4788031b3197d97197b56be65961f290356c4f3bc9db3406f30444b9"
	first, err := previewObjectFor(0, nil, sha256.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Format(first.digest); got != firstDigest {
		t.Fatalf("the first preview object's digest = %s, want %s: the codec's framed bytes must stay pinned", got, firstDigest)
	}

	var builder previewBuilder
	for _, ordinal := range ordinals {
		typ, size := previewObjectType(ordinal), previewObjectSize(ordinal)
		refs := previewObjectReferences(ordinal, references)
		want, err := previewEnvelopeByCodec(typ, ordinal, size, refs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := builder.objectFor(ordinal, references, sha256.New())
		if err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
		if !bytes.Equal(got.data, want) {
			t.Fatalf("ordinal %d: the reused-fill frame differs from the per-ordinal encoding (%d vs %d bytes)", ordinal, len(got.data), len(want))
		}
		wantDigest, err := sha256.New().Digest(bytes.NewReader(want))
		if err != nil {
			t.Fatal(err)
		}
		if !got.digest.Equal(wantDigest) {
			t.Fatalf("ordinal %d: digest = %s, want %s", ordinal, sha256.Format(got.digest), sha256.Format(wantDigest))
		}
	}
}
