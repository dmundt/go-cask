package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

const defaultPreviewObjectCount = 500

var previewObjectTypes = []string{"blob@1", "text@1", "json@1", "note@1", "manifest@1"}
var previewObjectSizes = []int{128, 986, 2200, 48 << 10, 384 << 10, 1200 << 10}

// opSeedPreview writes deterministic, valid envelope objects suitable for
// exercising the object browser's filtering, sorting, and pagination.
func opSeedPreview(ctx context.Context, t *target, args []string) error {
	flags := flag.NewFlagSet("seed-preview", flag.ContinueOnError)
	count := flags.Int("count", defaultPreviewObjectCount, "number of preview objects (1-10000)")
	if err := flags.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if flags.NArg() != 0 {
		return usagef("seed-preview accepts flags only")
	}
	if *count < 1 || *count > 10000 {
		return usagef("count must be between 1 and 10000, got %d", *count)
	}

	added, deduplicated, err := seedPreview(ctx, t.raw, *count)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "preview objects: added %d, deduplicated %d\n", added, deduplicated)
	return err
}

func seedPreview(ctx context.Context, raw *fs.Backend, count int) (int, int, error) {
	added, deduplicated := 0, 0
	digests := make([]cas.Digest, 0, count)
	for ordinal := range count {
		object := previewObjectFor(ordinal, digests)
		if previewCorruptOrdinal(ordinal) {
			// Store bytes that do not hash to their own address, so Verify
			// genuinely fails instead of the viewer faking a corrupt status.
			exists, err := raw.Exists(ctx, object.digest)
			if err != nil {
				return 0, 0, fmt.Errorf("check preview object %d: %w", object.ordinal, err)
			}
			if exists {
				deduplicated++
			} else {
				if err := raw.Put(ctx, object.digest, bytes.NewReader(previewTamper(object.data))); err != nil {
					return 0, 0, fmt.Errorf("seed corrupt preview object %d: %w", object.ordinal, err)
				}
				added++
			}
			digests = append(digests, object.digest)
			continue
		}
		data := object.data
		_, exists, err := localPut(ctx, raw, bytes.NewReader(data))
		if err != nil {
			return 0, 0, fmt.Errorf("seed preview object %d: %w", object.ordinal, err)
		}
		if exists {
			deduplicated++
		} else {
			added++
		}
		digests = append(digests, object.digest)
	}
	return added, deduplicated, nil
}

// previewCorruptOrdinal marks preview objects whose stored bytes are tampered.
// The chosen ordinals sit inside the reachable blocks that previewReferences
// walks from its roots, so the browser shows corrupt objects that are not
// orphaned and the two status axes stay visibly independent.
func previewCorruptOrdinal(ordinal int) bool {
	return ordinal%8 == 1
}

// previewTamper flips a payload byte, leaving the envelope header intact so the
// object still reports its type while failing hash verification.
func previewTamper(data []byte) []byte {
	tampered := append([]byte(nil), data...)
	tampered[len(tampered)-1] ^= 0xff
	return tampered
}

type previewObject struct {
	ordinal    int
	digest     cas.Digest
	references []cas.Digest
	data       []byte
}

func previewObjectFor(ordinal int, digests []cas.Digest) previewObject {
	references := previewObjectReferences(digests)
	data := previewEnvelope(
		previewObjectTypes[ordinal%len(previewObjectTypes)],
		ordinal,
		previewObjectSizes[ordinal%len(previewObjectSizes)],
		references,
	)
	return previewObject{
		ordinal:    ordinal,
		digest:     sha256.Of(data),
		references: references,
		data:       data,
	}
}

func previewObjectReferences(digests []cas.Digest) []cas.Digest {
	count := min(len(digests), len(digests)%4)
	if count == 0 {
		return nil
	}
	references := make([]cas.Digest, 0, count)
	for offset := range count {
		references = append(references, digests[len(digests)-1-offset])
	}
	return references
}

func previewEnvelope(typ string, ordinal, payloadSize int, references []cas.Digest) []byte {
	referenceText := ""
	for _, reference := range references {
		referenceText += " " + reference.String()
	}
	header := fmt.Sprintf("preview object %03d: %s refs:%s", ordinal, typ, referenceText)
	payload := bytes.Repeat([]byte{byte(ordinal)}, max(payloadSize, len(header)))
	copy(payload, header)

	var lengthBuffer [binary.MaxVarintLen64]byte
	typeLength := binary.PutUvarint(lengthBuffer[:], uint64(len(typ)))
	payloadLength := binary.PutUvarint(lengthBuffer[:], uint64(len(payload)))
	data := make([]byte, 1+typeLength+len(typ)+payloadLength+len(payload))
	data[0] = 1
	offset := 1
	offset += binary.PutUvarint(data[offset:], uint64(len(typ)))
	offset += copy(data[offset:], typ)
	offset += binary.PutUvarint(data[offset:], uint64(len(payload)))
	copy(data[offset:], payload)
	return data
}

type previewReferenceIndex struct {
	inbound   map[string][]cas.Digest
	outbound  map[string][]cas.Digest
	reachable map[string]bool
}

func (i *previewReferenceIndex) Inbound(target cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.inbound[target.String()]...)
}

func (i *previewReferenceIndex) Outbound(source cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.outbound[source.String()]...)
}

func (i *previewReferenceIndex) IsReachable(digest cas.Digest) bool {
	return i.reachable[digest.String()]
}

func previewReferences(ctx context.Context, raw *fs.Backend) (*previewReferenceIndex, error) {
	index := &previewReferenceIndex{
		inbound:   make(map[string][]cas.Digest),
		outbound:  make(map[string][]cas.Digest),
		reachable: make(map[string]bool),
	}
	digests := make([]cas.Digest, 0, 10000)
	for ordinal := range 10000 {
		object := previewObjectFor(ordinal, digests)
		exists, err := raw.Exists(ctx, object.digest)
		if err != nil {
			return nil, fmt.Errorf("check preview object %d: %w", object.ordinal, err)
		}
		if !exists {
			break
		}
		index.outbound[object.digest.String()] = append([]cas.Digest(nil), object.references...)
		for _, target := range object.references {
			index.inbound[target.String()] = append(index.inbound[target.String()], object.digest)
		}
		digests = append(digests, object.digest)
	}
	if len(index.outbound) == 0 {
		return nil, nil
	}
	var visit func(cas.Digest)
	visit = func(digest cas.Digest) {
		if index.reachable[digest.String()] {
			return
		}
		index.reachable[digest.String()] = true
		for _, reference := range index.outbound[digest.String()] {
			visit(reference)
		}
	}
	for root := 3; root < len(digests); root += 8 {
		visit(digests[root])
	}
	return index, nil
}
