package main

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
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

// TestPreviewWalkAllocatesOnePayloadPerOrdinal pins the allocation floor of the
// viewer's startup walk (`cask web` calls previewReferences on every start,
// web.go), against a ceiling expressed in payloads per ordinal.
//
// Every ordinal's envelope carries a synthetic payload built from
// previewObjectSize, and the frame that carries it is a copy of those bytes in
// one contiguous slice (cas.EncodeEnvelope). Materializing the payload separately
// for every ordinal therefore costs two payloads per ordinal — the payload and
// the frame that holds its copy — where one is enough: the walk can fill one
// buffer and reuse it across the whole run, because only the frame outlives the
// ordinal (go-cask#374).
//
// The ceiling is 1.5 payloads per ordinal: the frame (one payload plus a few
// dozen header bytes) and the index's own bounded bookkeeping fit well inside
// it, while a second full payload per ordinal — the defect — sits at 2.0 and
// fails by construction. Measured on go1.27.1 windows/amd64 over the walk's
// documented bound of 10000 ordinals: 5 641 133 096 B total, 564 113 B/ordinal,
// 2.02 payloads before the fix, and 2 850 468 320 B total, 285 046 B/ordinal,
// 1.02 payloads after — 2.79 GB (49%) less. The benchmark suite is manual and CI
// runs no -bench, so this test is the guard that keeps the duplication gone.
func TestPreviewWalkAllocatesOnePayloadPerOrdinal(t *testing.T) {
	var totalPayload int
	for ordinal := range maxPreviewCount {
		totalPayload += previewObjectSize(ordinal)
	}
	meanPayload := totalPayload / maxPreviewCount
	budget := int64(meanPayload) * 3 / 2

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
		t.Fatalf("the preview walk allocated %d B/ordinal, want <= %d B/ordinal = 1.5 payloads: each ordinal's payload is being materialized twice — once for itself and once into the frame cas.EncodeEnvelope returns (go-cask#374)",
			perOrdinal, budget)
	}
}

// previewEnvelopeByRepeat frames one preview object the way the walk did before
// go-cask#374: a payload allocated for the ordinal with bytes.Repeat and then
// copied whole into the frame cas.EncodeEnvelope returns. It is deliberately the
// allocating form — the reference the reused-buffer builder must reproduce byte
// for byte, since a preview object's digest is the address the store and the
// viewer must agree on (cli.md §2).
func previewEnvelopeByRepeat(typ string, ordinal, payloadSize int, references []cas.Digest) ([]byte, error) {
	referenceText := ""
	for _, reference := range references {
		referenceText += " " + reference.String()
	}
	header := fmt.Sprintf("preview object %03d: %s refs:%s", ordinal, typ, referenceText)
	payload := bytes.Repeat([]byte{byte(ordinal)}, max(payloadSize, len(header)))
	copy(payload, header)
	return cas.EncodeEnvelope(previewCodecTag, typ, payload)
}

// TestPreviewBuilderReproducesThePerOrdinalFraming proves the reused payload
// buffer is a pure allocation fix (go-cask#374): for a sequence of ordinals that
// covers every size in the table, a byte that wraps past 255, and a large payload
// followed by the smallest one — the case a stale buffer would corrupt — the
// builder's frame and digest are byte-identical to the per-ordinal framing the
// walk used before, and to the digests that framing produced.
func TestPreviewBuilderReproducesThePerOrdinalFraming(t *testing.T) {
	// A fixed reference list, so every object carries the references its ordinal
	// asks for (previewObjectReferences) whatever the digest chain would have
	// handed it.
	references := make([]cas.Digest, 0, 16)
	for i := range 16 {
		references = append(references, sha256.Of(fmt.Appendf(nil, "preview reference %d", i)))
	}
	// Ascending ordinals cycle the sizes 128 B → 1.2 MiB and shrink again, so the
	// buffer is grown and reused; 0 after 9995 is the shrink case (1.2 MiB buffer,
	// 128 B payload), and 1000/1001 cover ordinals whose byte() is not their own.
	ordinals := make([]int, 0, 28)
	for ordinal := range 24 {
		ordinals = append(ordinals, ordinal)
	}
	ordinals = append(ordinals, 1000, 1001, 9995, 9999, 0)

	// The anchor: ordinal 0 carries no references whatever the digest chain is
	// (previewObjectReferences asks for min(len(digests), 0)), so its address is a
	// fixed constant — the one every seeded preview store holds for the first
	// object, and the one the per-ordinal framing produced before go-cask#374. It
	// is short enough to check by hand against the TLV layout cas-core §8 d1
	// documents (a 146-byte frame).
	const firstDigest = "sha256:c5f1b05d7efe5628c020354295fabfb62db4206e615182cb3cefbcb9a7e94f24"
	first, err := previewObjectFor(0, nil, sha256.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Format(first.digest); got != firstDigest {
		t.Fatalf("the first preview object's digest = %s, want %s: the framed bytes must not change (go-cask#374)", got, firstDigest)
	}

	var builder previewBuilder
	for _, ordinal := range ordinals {
		typ, size := previewObjectType(ordinal), previewObjectSize(ordinal)
		refs := previewObjectReferences(ordinal, references)
		want, err := previewEnvelopeByRepeat(typ, ordinal, size, refs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := builder.objectFor(ordinal, references, sha256.New())
		if err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
		if !bytes.Equal(got.data, want) {
			t.Fatalf("ordinal %d: the reused-payload frame differs from the per-ordinal framing (%d vs %d bytes)", ordinal, len(got.data), len(want))
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
