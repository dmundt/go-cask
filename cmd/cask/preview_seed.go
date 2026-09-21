package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
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
	for ordinal := range count {
		data := previewEnvelope(
			previewObjectTypes[ordinal%len(previewObjectTypes)],
			ordinal,
			previewObjectSizes[ordinal%len(previewObjectSizes)],
		)
		_, exists, err := localPut(ctx, raw, bytes.NewReader(data))
		if err != nil {
			return 0, 0, fmt.Errorf("seed preview object %d: %w", ordinal, err)
		}
		if exists {
			deduplicated++
		} else {
			added++
		}
	}
	return added, deduplicated, nil
}

func previewEnvelope(typ string, ordinal, payloadSize int) []byte {
	payload := bytes.Repeat([]byte{byte(ordinal)}, payloadSize)
	copy(payload, fmt.Sprintf("preview object %03d: %s", ordinal, typ))

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
