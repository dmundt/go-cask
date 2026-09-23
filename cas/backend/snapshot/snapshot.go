// Package snapshot exports and imports raw objects through any cas.Backend.
//
// The archive is a deterministic, versioned transport format for tests,
// replay, and backend migration. It contains only digest bytes and payloads;
// typed codecs and hashers remain outside this package.
package snapshot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
)

var magic = [8]byte{'C', 'A', 'S', 'K', 'S', 'N', 'A', 'P'}

const (
	version          uint16 = 1
	headerSize              = 8 + 2 + 8
	recordHeaderSize        = 16
	maxDigestSize           = 1 << 20
)

// Export writes all objects from src to w in digest order. Each payload is
// buffered while its record length is determined; the archive itself is
// written incrementally. src is not locked by this operation, so callers
// should avoid concurrent mutations when they need a point-in-time export.
func Export(ctx context.Context, src cas.Backend, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if src == nil {
		return errors.New("snapshot: source backend is nil")
	}
	if w == nil {
		return errors.New("snapshot: writer is nil")
	}

	digests, err := src.List(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: list source objects: %w", err)
	}
	// Hex order equals byte order, so comparing raw digest bytes sorts exactly
	// like comparing the rendered hex strings — without allocating two strings
	// per comparison.
	slices.SortFunc(digests, func(a, b cas.Digest) int { return bytes.Compare(a, b) })

	var header [headerSize]byte
	copy(header[:8], magic[:])
	binary.BigEndian.PutUint16(header[8:10], version)
	binary.BigEndian.PutUint64(header[10:], uint64(len(digests)))
	if err := backend.WriteAll(ctx, w, header[:]); err != nil {
		return fmt.Errorf("snapshot: write header: %w", err)
	}

	var lengths [recordHeaderSize]byte
	for _, digest := range digests {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := cas.CheckDigest(digest, "snapshot: export"); err != nil {
			return err
		}
		rc, err := src.Get(ctx, digest)
		if err != nil {
			return fmt.Errorf("snapshot: get %s: %w", digest, err)
		}
		payload, readErr := io.ReadAll(backend.ContextReader{Ctx: ctx, R: rc})
		closeErr := rc.Close()
		if readErr != nil {
			return fmt.Errorf("snapshot: read %s: %w", digest, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("snapshot: close %s: %w", digest, closeErr)
		}

		binary.BigEndian.PutUint64(lengths[:8], uint64(len(digest)))
		binary.BigEndian.PutUint64(lengths[8:], uint64(len(payload)))
		if err := backend.WriteAll(ctx, w, lengths[:]); err != nil {
			return fmt.Errorf("snapshot: write record lengths: %w", err)
		}
		if err := backend.WriteAll(ctx, w, digest); err != nil {
			return fmt.Errorf("snapshot: write digest %s: %w", digest, err)
		}
		if err := backend.WriteAll(ctx, w, payload); err != nil {
			return fmt.Errorf("snapshot: write payload %s: %w", digest, err)
		}
	}
	return nil
}

// Import reads an archive and writes its objects to dst. The archive is
// validated record by record, including duplicate and invalid digests.
// Generic imports are not atomic: a later read or Put failure can leave
// objects already written to dst.
func Import(ctx context.Context, dst cas.Backend, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dst == nil {
		return errors.New("snapshot: destination backend is nil")
	}
	if r == nil {
		return errors.New("snapshot: reader is nil")
	}

	var header [headerSize]byte
	if err := backend.ReadAll(ctx, r, header[:]); err != nil {
		return fmt.Errorf("snapshot: read header: %w", err)
	}
	if !bytes.Equal(header[:8], magic[:]) {
		return errors.New("snapshot: invalid magic")
	}
	if binary.BigEndian.Uint16(header[8:10]) != version {
		return errors.New("snapshot: unsupported version")
	}
	count := binary.BigEndian.Uint64(header[10:])
	if count > uint64(math.MaxInt) {
		return errors.New("snapshot: object count is too large")
	}

	seen := make(map[string]struct{})
	var lengths [recordHeaderSize]byte
	for range count {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := backend.ReadAll(ctx, r, lengths[:]); err != nil {
			return fmt.Errorf("snapshot: read record lengths: %w", err)
		}
		digestSize := binary.BigEndian.Uint64(lengths[:8])
		payloadSize := binary.BigEndian.Uint64(lengths[8:])
		if digestSize > maxDigestSize || digestSize > uint64(math.MaxInt) {
			return errors.New("snapshot: invalid digest size")
		}
		// math.MaxInt is the largest int, so on every platform it also caps
		// what the payload reader can address.
		if payloadSize > uint64(math.MaxInt) {
			return errors.New("snapshot: payload is too large")
		}
		digest := make([]byte, int(digestSize))
		if err := backend.ReadAll(ctx, r, digest); err != nil {
			return fmt.Errorf("snapshot: read digest: %w", err)
		}
		d := cas.NewDigest(digest)
		if err := cas.CheckDigest(d, "snapshot: import"); err != nil {
			return err
		}
		key := string(d)
		if _, ok := seen[key]; ok {
			return errors.New("snapshot: duplicate digest")
		}
		seen[key] = struct{}{}

		payload, err := backend.ReadPayload(ctx, r, payloadSize)
		if err != nil {
			return fmt.Errorf("snapshot: read payload for %s: %w", d, err)
		}
		if err := dst.Put(ctx, d, bytes.NewReader(payload)); err != nil {
			return fmt.Errorf("snapshot: put %s: %w", d, err)
		}
	}

	var extra [1]byte
	n, err := r.Read(extra[:])
	if n != 0 {
		return errors.New("snapshot: trailing data")
	}
	if err != io.EOF {
		if err == nil {
			return errors.New("snapshot: trailing data")
		}
		return fmt.Errorf("snapshot: check trailing data: %w", err)
	}
	return nil
}
