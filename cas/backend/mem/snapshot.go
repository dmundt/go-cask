package memory

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
)

var snapshotMagic = [8]byte{'C', 'A', 'S', 'K', 'M', 'E', 'M', 0}

const (
	snapshotVersion       uint16 = 1
	snapshotHeaderSize           = 8 + 2 + 8 + 8
	maxSnapshotDigestSize        = 1 << 20
)

// Snapshot writes a deterministic, versioned binary snapshot of all objects
// to w. It stores raw digests and payloads, without involving a typed codec or
// hash algorithm. The backend remains readable and unchanged if writing fails.
func (m *Backend) Snapshot(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w == nil {
		return errors.New("mem: snapshot writer is nil")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.objects))
	var total uint64
	for key, data := range m.objects {
		if key == "" {
			continue
		}
		keys = append(keys, key)
		total += uint64(len(data))
	}
	sort.Strings(keys)

	var header [snapshotHeaderSize]byte
	copy(header[:8], snapshotMagic[:])
	binary.BigEndian.PutUint16(header[8:10], snapshotVersion)
	binary.BigEndian.PutUint64(header[10:18], uint64(len(keys)))
	binary.BigEndian.PutUint64(header[18:26], total)
	if err := backend.WriteAll(ctx, w, header[:]); err != nil {
		return fmt.Errorf("mem: write snapshot header: %w", err)
	}

	var lengths [16]byte
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := m.objects[key]
		binary.BigEndian.PutUint64(lengths[:8], uint64(len(key)))
		binary.BigEndian.PutUint64(lengths[8:], uint64(len(data)))
		if err := backend.WriteAll(ctx, w, lengths[:]); err != nil {
			return fmt.Errorf("mem: write snapshot record lengths: %w", err)
		}
		if err := backend.WriteAll(ctx, w, []byte(key)); err != nil {
			return fmt.Errorf("mem: write snapshot digest: %w", err)
		}
		if err := backend.WriteAll(ctx, w, data); err != nil {
			return fmt.Errorf("mem: write snapshot payload: %w", err)
		}
	}
	return nil
}

// Restore replaces all objects with the complete snapshot read from r. The
// current state remains unchanged when decoding, validation, or size checks
// fail. A configured WithMaxSize limit applies to the restored state. Record
// sizes come from the archive header, so payloads are read through a bounded
// reader (backend.ReadPayload) and never allocated from the declared size.
func (m *Backend) Restore(ctx context.Context, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return errors.New("mem: snapshot reader is nil")
	}

	var header [snapshotHeaderSize]byte
	if err := backend.ReadAll(ctx, r, header[:]); err != nil {
		return fmt.Errorf("mem: read snapshot header: %w", err)
	}
	if !bytes.Equal(header[:8], snapshotMagic[:]) {
		return errors.New("mem: invalid snapshot magic")
	}
	if binary.BigEndian.Uint16(header[8:10]) != snapshotVersion {
		return errors.New("mem: unsupported snapshot version")
	}
	count := binary.BigEndian.Uint64(header[10:18])
	declaredTotal := binary.BigEndian.Uint64(header[18:26])
	if count > uint64(math.MaxInt) {
		return errors.New("mem: snapshot object count is too large")
	}
	if declaredTotal > uint64(math.MaxInt) {
		return errors.New("mem: snapshot size is too large")
	}
	if m.maxBytes > 0 && declaredTotal > uint64(m.maxBytes) {
		return fmt.Errorf("mem: snapshot exceeds max size %d bytes", m.maxBytes)
	}

	objects := make(map[string][]byte)
	var total uint64
	var lengths [16]byte
	for range count {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := backend.ReadAll(ctx, r, lengths[:]); err != nil {
			return fmt.Errorf("mem: read snapshot record lengths: %w", err)
		}
		digestSize := binary.BigEndian.Uint64(lengths[:8])
		payloadSize := binary.BigEndian.Uint64(lengths[8:])
		if digestSize == 0 || digestSize > maxSnapshotDigestSize || digestSize > uint64(math.MaxInt) {
			return errors.New("mem: invalid snapshot digest size")
		}
		// math.MaxInt is the largest int, so on every platform it also caps
		// what the payload reader can address.
		if payloadSize > uint64(math.MaxInt) {
			return errors.New("mem: invalid snapshot payload size")
		}
		if payloadSize > uint64(math.MaxInt)-total {
			return errors.New("mem: snapshot size overflows")
		}
		total += payloadSize
		if total > declaredTotal {
			return errors.New("mem: snapshot payload exceeds declared size")
		}
		if m.maxBytes > 0 && total > uint64(m.maxBytes) {
			return fmt.Errorf("mem: snapshot exceeds max size %d bytes", m.maxBytes)
		}

		digest := make([]byte, int(digestSize))
		if err := backend.ReadAll(ctx, r, digest); err != nil {
			return fmt.Errorf("mem: read snapshot digest: %w", err)
		}
		d := cas.NewDigest(digest)
		if d.IsZero() {
			return errors.New("mem: snapshot contains an empty digest")
		}
		key := string(d)
		if _, exists := objects[key]; exists {
			return errors.New("mem: snapshot contains duplicate digest")
		}
		payload, err := backend.ReadPayload(ctx, r, payloadSize)
		if err != nil {
			return fmt.Errorf("mem: read snapshot payload: %w", err)
		}
		objects[key] = payload
	}
	if total != declaredTotal {
		return errors.New("mem: snapshot total size mismatch")
	}
	var extra [1]byte
	n, err := backend.ContextReader{Ctx: ctx, R: r}.Read(extra[:])
	if n != 0 {
		return errors.New("mem: snapshot trailing data")
	}
	if err != io.EOF {
		if err == nil {
			return errors.New("mem: snapshot trailing data")
		}
		return fmt.Errorf("mem: check snapshot trailing data: %w", err)
	}

	m.mu.Lock()
	m.objects = objects
	m.usedBytes = int64(total)
	m.mu.Unlock()
	return nil
}
