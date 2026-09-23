// Package index provides the listing/metadata helpers shared by the cask CLI
// and the viewer: pagination over stored digests and best-effort type
// detection from the self-describing TLV envelope (cas-core §8 decision 1).
package index

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// Paginate returns the items in the [offset, offset+limit) window, bounded to
// the slice (list pagination per defaults §3). A negative offset reads as 0
// and a non-positive limit yields no items; callers reject out-of-range input
// (cli/api-design §9) rather than relying on the clamp.
func Paginate[T any](items []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) || limit <= 0 {
		return nil
	}
	hi := offset + limit
	if hi > len(items) || hi < offset { // overflow guard
		hi = len(items)
	}
	return items[offset:hi]
}

// EnvelopeType extracts the versioned type name ("blob@1", …) from the
// self-describing TLV envelope (cas-core §8 decision 1) on a best-effort
// basis; "" when the bytes are not an envelope (raw objects have no type).
// A legacy unversioned type name reads back with "@1" appended (object-
// versioning §2).
//
// Only the header — [version][uvarint codecLen][codec][uvarint typeLen][type] —
// is inspected, so a truncated object prefix (the viewer reads a bounded
// prefix, not the whole object) still yields its type. It delegates the header
// layout to cas.EnvelopeType, which owns the format, and reports that parser's
// error as the absent type: an unreadable header is exactly what an untyped
// object looks like to a best-effort sniff.
func EnvelopeType(data []byte) string {
	typ, err := cas.EnvelopeType(data)
	if err != nil {
		return ""
	}
	return typ
}

// headerPrefixLimit bounds the bytes HeaderType reads. The TLV header is a few
// dozen bytes for any realistic codec tag and type name, so this is generous;
// it exists only so a hostile or damaged object cannot make a header read grow
// with the object (the core's own per-field ceiling is 4 KiB).
const headerPrefixLimit = 8 << 10

// HeaderType reads only the envelope header of the object at d and returns its
// versioned type name ("blob@1", …); "" when the bytes are not an envelope.
//
// It is the one place the viewer's index, the inspector, and the cask CLI learn
// an object's type from stored bytes — each used to read its own bounded prefix
// (4 KiB in two places, 64 KiB in a third) and sniff it.
//
// The read is a bounded prefix parsed by the index's best-effort EnvelopeType,
// not cas.PeekType: a store legitimately holds raw, un-enveloped objects
// (`cask put` writes the file's own bytes), and PeekType reports anything that
// is not a valid header as cas.ErrCorrupt — which would relabel every raw object
// as damaged. A non-nil error here therefore means "the object could not be
// read", never "these bytes are not an envelope header".
func HeaderType(ctx context.Context, backend cas.Backend, d cas.Digest) (string, error) {
	rc, err := backend.Get(ctx, d)
	if err != nil {
		return "", err
	}
	prefix, err := io.ReadAll(io.LimitReader(rc, headerPrefixLimit))
	if err != nil {
		_ = rc.Close() // the read error is the one worth reporting
		return "", fmt.Errorf("cas: read object header: %w", err)
	}
	if err := rc.Close(); err != nil {
		return "", fmt.Errorf("cas: close object header reader: %w", err)
	}
	return EnvelopeType(prefix), nil
}

// Entry is the immutable metadata used by the viewer query path. Keeping the
// result of one store walk together avoids re-opening and re-statting every
// object for each filter, sort, or pagination request.
type Entry struct {
	// Digest identifies the object.
	Digest cas.Digest
	// Type is the decoded envelope type.
	Type string
	// Size is the object's stored byte count.
	Size int64
	// Written is the object's backend modification time.
	Written time.Time
	// Unreadable reports whether metadata could not be read.
	Unreadable bool
}

// Snapshot is a point-in-time metadata index. Callers must treat Entries as
// read-only; a new snapshot is built when the backing store changes.
type Snapshot struct {
	// Entries contains metadata for every indexed object.
	Entries []Entry
	// Types lists discovered object types.
	Types []string
	// Total is the number of indexed objects.
	Total int
	// Bytes is the total stored size of indexed objects.
	Bytes int64
}

// metadataSource is what the index needs from a backend: the byte contract plus
// the physical per-object metadata. cas.Statter names exactly Size and ModTime,
// so the interface embeds it rather than repeating the two signatures — a
// backend that satisfies the core's optional capability satisfies this too.
type metadataSource interface {
	cas.Backend
	cas.Statter
}

// BuildSnapshot scans source once and records bounded envelope metadata.
// Unreadable records remain indexed so the browser can report them without
// repeatedly retrying a broken object during one request burst.
func BuildSnapshot(ctx context.Context, source metadataSource) (*Snapshot, error) {
	digests, err := source.List(ctx)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Entries: make([]Entry, 0, len(digests)), Total: len(digests)}
	types := make(map[string]struct{})
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := Entry{Digest: d}
		if typ, err := HeaderType(ctx, source, d); err != nil {
			e.Unreadable = true
		} else {
			e.Type = typ
		}
		if e.Size, err = source.Size(ctx, d); err != nil {
			e.Unreadable = true
		} else {
			s.Bytes += e.Size
		}
		if e.Written, err = source.ModTime(ctx, d); err != nil {
			e.Unreadable = true
		}
		if e.Type != "" {
			types[e.Type] = struct{}{}
		}
		s.Entries = append(s.Entries, e)
	}
	s.Types = make([]string, 0, len(types))
	for typ := range types {
		s.Types = append(s.Types, typ)
	}
	slices.Sort(s.Types)
	return s, nil
}
