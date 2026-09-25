package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/store"
)

const defaultPreviewObjectCount = 500

// maxPreviewCount bounds `seed-preview -count` (cli.md §2) and the number of
// ordinals the preview walk probes, so handing out the preview data and reading
// it back agree on one limit.
const maxPreviewCount = 10000

// previewBlockSize is the number of objects in one preview graph block. Each
// block carries one Root, orphan members with inbound edges, and one
// detached entry (cli.md §2).
const previewBlockSize = 8

// previewRootOffset is the in-block offset of the reachable Root: the
// member no other member of its block references (previewRootOrdinal).
const previewRootOffset = 3

// previewDetachedOffset is the in-block offset of the detached orphan: the last
// member, which no later sibling references back (previewDetachedOrdinal).
const previewDetachedOffset = 7

// previewCorruptOffset is the in-block offset seeded with tampered bytes: it
// sits inside a reachable segment, so the corrupt and orphaned states stay
// independent (cli.md §2).
const previewCorruptOffset = 1

// errNoPreviewGraph reports that the store holds no known preview graph, so the
// viewer has no reference source for it (cli.md §2). It is not a failure: an
// ordinary store simply has no preview data, and its references stay
// unavailable.
var errNoPreviewGraph = errors.New("store holds no preview graph")

// previewObjectType returns the versioned type name of the preview object with
// this ordinal, cycling the representative types. The table is a value returned
// to the caller, so there is no mutable package-level state to append to or
// share.
func previewObjectType(ordinal int) string {
	types := [...]string{"blob@1", "text@1", "json@1", "note@1", "manifest@1"}
	return types[ordinal%len(types)]
}

// previewObjectSize returns the payload size of the preview object with this
// ordinal, cycling representative sizes (48 KiB up to 1.2 MiB) so the browser
// has varied sizes to sort and paginate.
func previewObjectSize(ordinal int) int {
	sizes := [...]int{128, 986, 2200, 48 << 10, 384 << 10, 1200 << 10}
	return sizes[ordinal%len(sizes)]
}

// seedPreviewArgs holds seed-preview's flag values.
type seedPreviewArgs struct {
	count         int
	hashAlgorithm string
}

// seedPreviewFlags registers seed-preview's flags over a; opSeedPreview and the
// command table both use it, so the accepted and the documented flags are one
// set (cli.md §2, §4).
func seedPreviewFlags(a *seedPreviewArgs) *flag.FlagSet {
	flags := newFlagSet("seed-preview")
	flags.IntVar(&a.count, "count", defaultPreviewObjectCount, "number of preview objects (1-10000)")
	flags.StringVar(&a.hashAlgorithm, "hash-algo", sha256.Name, hashAlgoUsage)
	return flags
}

// opSeedPreview writes deterministic, valid envelope objects suitable for
// exercising the object browser's filtering, sorting, and pagination.
//
// The digest algorithm is selectable because the preview graph is addressed by
// the viewer's hasher: seeding with one algorithm and reading with another
// yields no graph at all (every probe misses), so `seed-preview -hash-algo`
// must match `cask web -hash-algo`.
func opSeedPreview(ctx context.Context, t *store.Store, args []string) error {
	var a seedPreviewArgs
	flags := seedPreviewFlags(&a)
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usagef("seed-preview accepts flags only")
	}
	if a.count < 1 || a.count > maxPreviewCount {
		return usagef("count must be between 1 and %d, got %d", maxPreviewCount, a.count)
	}
	algorithm, err := lookupDigestAlgorithm(a.hashAlgorithm)
	if err != nil {
		return usagef("invalid hash algorithm %q: %v", a.hashAlgorithm, err)
	}

	added, deduplicated, err := seedPreview(ctx, t, algorithm.hasher, a.count)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "preview objects: added %d, deduplicated %d\n", added, deduplicated)
	return err
}

func seedPreview(ctx context.Context, backend cas.Backend, hasher cas.Hasher, count int) (int, int, error) {
	added, deduplicated := 0, 0
	digests := make([]cas.Digest, 0, count)
	for ordinal := range count {
		object, err := previewObjectFor(ordinal, digests, hasher)
		if err != nil {
			return 0, 0, fmt.Errorf("build preview object %d: %w", ordinal, err)
		}
		exists, err := backend.Exists(ctx, object.digest)
		if err != nil {
			return 0, 0, fmt.Errorf("check preview object %d: %w", object.ordinal, err)
		}
		if exists {
			deduplicated++
			digests = append(digests, object.digest)
			continue
		}
		data := object.data
		if previewCorruptOrdinal(ordinal) {
			// Store bytes that do not hash to their own address, so Verify
			// genuinely fails instead of the viewer faking a corrupt status.
			data = previewTamper(data)
		}
		if err := backend.Put(ctx, object.digest, bytes.NewReader(data)); err != nil {
			return 0, 0, fmt.Errorf("seed preview object %d: %w", object.ordinal, err)
		}
		added++
		digests = append(digests, object.digest)
	}
	return added, deduplicated, nil
}

// previewCorruptOrdinal marks preview objects whose stored bytes are tampered.
// The chosen ordinals sit inside the reachable blocks that previewReferences
// walks from its roots, so the browser shows corrupt objects that are not
// orphaned and the two status axes stay visibly independent.
func previewCorruptOrdinal(ordinal int) bool {
	return ordinal%previewBlockSize == previewCorruptOffset
}

// previewTamper flips a payload byte, leaving the envelope header intact so the
// object still reports its type while failing hash verification.
func previewTamper(data []byte) []byte {
	tampered := slices.Clone(data)
	tampered[len(tampered)-1] ^= 0xff
	return tampered
}

type previewObject struct {
	ordinal    int
	digest     cas.Digest
	references []cas.Digest
	data       []byte
}

func previewObjectFor(ordinal int, digests []cas.Digest, hasher cas.Hasher) (previewObject, error) {
	references := previewObjectReferences(ordinal, digests)
	data, err := previewEnvelope(
		previewObjectType(ordinal),
		ordinal,
		previewObjectSize(ordinal),
		references,
	)
	if err != nil {
		return previewObject{}, err
	}
	digest, err := hasher.Digest(bytes.NewReader(data))
	if err != nil {
		return previewObject{}, err
	}
	return previewObject{
		ordinal:    ordinal,
		digest:     digest,
		references: references,
		data:       data,
	}, nil
}

// previewObjectReferences makes a preview block contain a reachable root at
// previewRootOffset (which doubles as a Root object: reachable with no inbound
// edge of its own), orphans with inbound references after it, and a detached
// orphan (no inbound edge at all) at previewDetachedOffset.
func previewObjectReferences(ordinal int, digests []cas.Digest) []cas.Digest {
	count := min(len(digests), ordinal%4)
	if count == 0 {
		return nil
	}
	references := make([]cas.Digest, 0, count)
	for offset := range count {
		references = append(references, digests[len(digests)-1-offset])
	}
	return references
}

// previewDetachedOrdinal reports whether ordinal seeds a detached preview
// object: it is not a root-reachable graph member and has no inbound edge.
// Within a preview block, only the last member (offset previewDetachedOffset)
// ends its block with no later sibling still referencing it back.
func previewDetachedOrdinal(ordinal int) bool {
	return ordinal%previewBlockSize == previewDetachedOffset
}

// previewRootOrdinal reports whether ordinal seeds a Root preview object: it
// is the reachable root of its preview block (offset previewRootOffset) and,
// because nothing in the block references a root, it also carries no inbound
// edge.
func previewRootOrdinal(ordinal int) bool {
	return ordinal%previewBlockSize == previewRootOffset
}

// previewCodecTag is the codec identity seeded preview objects carry. Their
// payload is a synthetic byte pattern no shipped codec produced, so the tag
// names the preview layer instead of claiming to be a decoder (cli.md §2): a
// reader sees a decodable-looking object whose codec identity is honest about
// where the bytes came from.
const previewCodecTag = "preview"

func previewEnvelope(typ string, ordinal, payloadSize int, references []cas.Digest) ([]byte, error) {
	referenceText := ""
	for _, reference := range references {
		referenceText += " " + reference.String()
	}
	header := fmt.Sprintf("preview object %03d: %s refs:%s", ordinal, typ, referenceText)
	payload := bytes.Repeat([]byte{byte(ordinal)}, max(payloadSize, len(header)))
	copy(payload, header)
	// The frame comes from the core's writer, not from a local copy of the
	// layout: the digest of these bytes must equal the digest of the bytes the
	// store would write, or the preview graph the viewer rebuilds points at
	// objects that do not exist (go-cask#187).
	return cas.EncodeEnvelope(previewCodecTag, typ, payload)
}

type previewReferenceIndex struct {
	inbound   map[string][]cas.Digest
	outbound  map[string][]cas.Digest
	reachable map[string]bool
}

// Inbound returns preview objects that reference target.
func (i *previewReferenceIndex) Inbound(target cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.inbound[target.String()]...)
}

// Outbound returns preview objects referenced by source.
func (i *previewReferenceIndex) Outbound(source cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.outbound[source.String()]...)
}

// IsReachable reports whether digest belongs to a preview root-reachable segment.
func (i *previewReferenceIndex) IsReachable(digest cas.Digest) bool {
	return i.reachable[digest.String()]
}

// previewReferences rebuilds the known deterministic preview graph over the
// objects the store holds, hashing every ordinal with the viewer's hasher (the
// digests must match the ones seeding wrote; a different algorithm would make
// every probe miss). It returns errNoPreviewGraph when the store holds none of
// them, so a caller reads "an ordinary store" from the error instead of having
// to treat a nil index as a non-error signal. It takes the minimal Backend
// contract, so the preview graph can be read from any backend.
//
// Digest accumulation is dense — every ordinal contributes its digest whether
// or not its bytes are present — because each object's digest is derived from
// the preceding ordinals: skipping an absent one would shift every later digest
// and lose the blocks after it. A missing ordinal therefore only drops that
// object's own edges, and the probe stops once a whole preview block is absent
// (a block is the graph's unit, so an empty one ends the graph) rather than at
// the first hole: `cask gc` removes the detached object of every block, and
// truncating there used to make the viewer report reachable objects as
// orphaned.
func previewReferences(ctx context.Context, backend cas.Backend, hasher cas.Hasher) (*previewReferenceIndex, error) {
	index := &previewReferenceIndex{
		inbound:   make(map[string][]cas.Digest),
		outbound:  make(map[string][]cas.Digest),
		reachable: make(map[string]bool),
	}
	digests := make([]cas.Digest, 0, maxPreviewCount)
	missingInBlock := 0
	for ordinal := range maxPreviewCount {
		object, err := previewObjectFor(ordinal, digests, hasher)
		if err != nil {
			return nil, fmt.Errorf("build preview object %d: %w", ordinal, err)
		}
		digests = append(digests, object.digest) // dense: later digests depend on it
		exists, err := backend.Exists(ctx, object.digest)
		if err != nil {
			return nil, fmt.Errorf("check preview object %d: %w", object.ordinal, err)
		}
		if !exists {
			missingInBlock++
			if missingInBlock >= previewBlockSize {
				break // an entire block is gone: the graph ends here
			}
			continue
		}
		missingInBlock = 0
		index.outbound[object.digest.String()] = append([]cas.Digest(nil), object.references...)
		for _, target := range object.references {
			index.inbound[target.String()] = append(index.inbound[target.String()], object.digest)
		}
	}
	if len(index.outbound) == 0 {
		return nil, errNoPreviewGraph
	}
	// The reachable closure is cas.Reachable: the same root-seeded expansion the
	// core documents for building the set Backend.GC/Backend.Prune require, so
	// seed-preview does not carry a second implementation of it (and the core's
	// is iterative, so a deep preview graph cannot recurse without bound).
	roots := make([]cas.Digest, 0, len(digests)/previewBlockSize+1)
	for root := previewRootOffset; root < len(digests); root += previewBlockSize {
		roots = append(roots, digests[root])
	}
	reachable, err := cas.Reachable(ctx, cas.RefListerFunc(func(_ context.Context, digest cas.Digest) ([]cas.Digest, error) {
		return index.outbound[digest.String()], nil
	}), roots)
	if err != nil {
		return nil, err
	}
	index.reachable = reachable
	return index, nil
}
