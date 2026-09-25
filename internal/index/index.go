// Package index provides the listing/metadata helpers shared by the cask CLI
// and the viewer: pagination over stored digests and best-effort type
// detection from the self-describing TLV envelope (cas-core §8 decision 1).
package index

import (
	"context"
	"errors"
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

// Header reads the envelope header of the object at d — its frame version, its
// codec identity tag and its versioned type name ("blob@1", …) — in one pass,
// and reports every field as absent (zero values) when the bytes are not an
// envelope this build can walk.
//
// It is the one place the viewer's index, the inspector, and the cask CLI learn
// an object's header from stored bytes. The three fields live in one walk: the
// read is cas.Header, which opens the object once and walks its header under a
// bound, so a CLI that lists a store, or a viewer that rebuilds its snapshot,
// pays no more for a 1 GiB object than for an empty one. The CLI, the viewer,
// cas/repo and the gitlike resolver therefore share one bounded header read
// instead of reading one prefix each (go-cask#319), and this layer carries no
// prefix limit of its own.
//
// Best-effort on purpose: a store legitimately holds raw, un-enveloped objects
// (`cask put` writes the file's own bytes), and cas.Header reports anything that
// is not a usable header as cas.ErrCorrupt — which would relabel every raw
// object as damaged. Damaged bytes therefore read here as "no header", while a
// non-nil error means the object could not be read at all.
func Header(ctx context.Context, backend cas.Backend, d cas.Digest) (version byte, codec, typeName string, err error) {
	version, codec, typeName, err = cas.Header(ctx, backend, d)
	if err != nil && errors.Is(err, cas.ErrCorrupt) {
		return 0, "", "", nil // not an envelope header; not damage either
	}
	return version, codec, typeName, err
}

// HeaderType is Header's type-only convenience, kept for the callers that report
// just the type (the inspector's metadata read).
func HeaderType(ctx context.Context, backend cas.Backend, d cas.Digest) (string, error) {
	_, _, typeName, err := Header(ctx, backend, d)
	return typeName, err
}

// Entry is the immutable metadata used by the viewer query path. Keeping the
// result of one store walk together avoids re-opening and re-statting every
// object for each filter, sort, or pagination request.
type Entry struct {
	// Digest identifies the object.
	Digest cas.Digest
	// Type is the decoded envelope type ("" when the bytes carry no header).
	Type string
	// Version is the envelope frame's leading version byte, 0 when the bytes
	// carry no header this build can walk.
	Version byte
	// Codec is the writing codec's identity tag, "" both when the frame carries
	// no identity (a version 1 frame, or a codec that declared no tag) and when
	// the bytes carry no header at all: the two are told apart by Version.
	Codec string
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
	// Versions lists the envelope frame versions present, ascending. An object
	// whose header could not be read contributes to none of these lists.
	Versions []byte
	// Codecs lists the writing codec tags present, ascending; "" is the
	// "unspecified" tag and is listed as such when some object carries it.
	Codecs []string
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
	versions := make(map[byte]struct{})
	codecs := make(map[string]struct{})
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := Entry{Digest: d}
		version, codec, typ, err := Header(ctx, source, d)
		if err != nil {
			e.Unreadable = true
		} else {
			e.Version, e.Codec, e.Type = version, codec, typ
		}
		if e.Size, err = source.Size(ctx, d); err != nil {
			e.Unreadable = true
		} else {
			s.Bytes += e.Size
		}
		if e.Written, err = source.ModTime(ctx, d); err != nil {
			e.Unreadable = true
		}
		// An object whose header did not parse contributes to no census list:
		// the viewer and the CLI must not invent a version, a codec or a type
		// for bytes they could not read.
		if e.Type != "" {
			types[e.Type] = struct{}{}
			versions[e.Version] = struct{}{}
			codecs[e.Codec] = struct{}{}
		}
		s.Entries = append(s.Entries, e)
	}
	s.Types = make([]string, 0, len(types))
	for typ := range types {
		s.Types = append(s.Types, typ)
	}
	slices.Sort(s.Types)
	s.Versions = make([]byte, 0, len(versions))
	for version := range versions {
		s.Versions = append(s.Versions, version)
	}
	slices.Sort(s.Versions)
	s.Codecs = make([]string, 0, len(codecs))
	for codec := range codecs {
		s.Codecs = append(s.Codecs, codec)
	}
	slices.Sort(s.Codecs)
	return s, nil
}
