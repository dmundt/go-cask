// Package index provides the listing/metadata helpers shared by the cask CLI
// and the viewer: pagination over stored digests and best-effort type
// detection from the self-describing TLV envelope (cas-core §8 decision 1).
package index

import (
	"context"
	"errors"
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
// object as damaged. Structural damage therefore reads here as "no header".
//
// A header that could not be *read* is not that case. cas.Header's walker wraps
// a read failure's cause under the same sentinel (cas.peekError), so the corrupt
// verdict that carries a foreign cause is returned as an error and the object is
// reported unreadable rather than untyped (Entry.Unreadable, viewer-design §3;
// go-cask#357). A non-nil error therefore always means the object could not be
// read at all, and a caller can never mistake a listed-but-unreadable object for
// an un-enveloped one.
func Header(ctx context.Context, backend cas.Backend, d cas.Digest) (version byte, codec, typeName string, err error) {
	version, codec, typeName, err = cas.Header(ctx, backend, d)
	switch {
	case err == nil:
		return version, codec, typeName, nil
	case unreadable(err):
		return 0, "", "", err
	default:
		return 0, "", "", nil // not an envelope header; not damage either
	}
}

// unreadable reports whether a cas.Header error says the object could not be
// read, as opposed to "these bytes are not a usable envelope header".
//
// Both arrive as cas.ErrCorrupt when the header walker refuses the bytes, and
// the backend's own error arrives untouched, so the two are told apart on the
// error chain: cas.peekError keeps a read failure's cause beneath the sentinel
// and wraps only the sentinel for a structural failure (an unsupported version,
// a length beyond the buffer, an empty type name). A tree whose every error is
// the sentinel or an end-of-stream marker is a verdict on the bytes; a foreign
// error anywhere in it is a read that failed.
func unreadable(err error) bool {
	if err == nil {
		return false
	}
	if !errors.Is(err, cas.ErrCorrupt) {
		return true // the backend's own open, close or read error
	}
	return !onlyCorrupt(err)
}

// onlyCorrupt reports whether every error in err's unwrap tree is cas.ErrCorrupt
// or an end-of-stream marker. It walks both unwrap shapes, because Go's fmt
// yields a multi-error wrapper for a format string with two %w verbs.
func onlyCorrupt(err error) bool {
	if err == nil {
		return true
	}
	if !errors.Is(err, cas.ErrCorrupt) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range multi.Unwrap() {
			if !onlyCorrupt(inner) {
				return false
			}
		}
		return true
	}
	return onlyCorrupt(errors.Unwrap(err))
}

// HeaderType is Header's type-only convenience, kept for the callers that report
// just the type (the inspector's metadata read).
func HeaderType(ctx context.Context, backend cas.Backend, d cas.Digest) (string, error) {
	_, _, typeName, err := Header(ctx, backend, d)
	return typeName, err
}

// UnspecifiedCodec is how every surface renders a frame that carries no codec
// identity: a version 1 envelope, a version 2 envelope whose codec declared no
// tag, or bytes with no walkable header at all (a raw object stored by `put`).
//
// It is the documented value rather than a blank, so "no codec" is never read as
// "the surface forgot to report it" (cli.md §2, viewer-design §3). It lives here,
// beside the Entry.Codec it labels, because both surfaces fill that field from
// the one header read this package performs (Entry, BuildSnapshot) — a second
// literal in either surface could drift from this one without anything failing.
const UnspecifiedCodec = "unspecified"

// CodecLabel renders a frame's codec identity tag for a surface: the tag as
// stored, or UnspecifiedCodec when the frame carries none.
//
// It is deliberately not version-aware. Bytes with no walkable header (Version
// 0) and a frame that carries no tag both report an empty tag here, so a caller
// that must tell them apart decides that from Entry.Version — the viewer's
// not-read marker is exactly such a decision, and it is the viewer's own.
func CodecLabel(codec string) string {
	if codec == "" {
		return UnspecifiedCodec
	}
	return codec
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
// backend that satisfies the core's optional capability satisfies this too. A
// source that ALSO implements cas.PhysicalStatter is asked once per object
// instead of twice (BuildSnapshot, go-cask#373).
type metadataSource interface {
	cas.Backend
	cas.Statter
}

// BuildSnapshot scans source once and records bounded envelope metadata.
// Unreadable records remain indexed so the browser can report them without
// repeatedly retrying a broken object during one request burst.
//
// Each object's size and modification time come from one physical read when the
// source implements cas.PhysicalStatter (fs and packfs do): the walk already had
// both values, and asking Size and then ModTime stats the same file twice
// (go-cask#373). A source without the capability is asked the two questions,
// which produces the same snapshot — the capability is an optimization, never a
// different answer.
func BuildSnapshot(ctx context.Context, source metadataSource) (*Snapshot, error) {
	digests, err := source.List(ctx)
	if err != nil {
		return nil, err
	}
	physical, hasPhysical := source.(cas.PhysicalStatter)
	s := &Snapshot{Entries: make([]Entry, 0, len(digests)), Total: len(digests)}
	types := make(map[string]struct{})
	versions := make(map[byte]struct{})
	codecs := make(map[string]struct{})
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := Entry{Digest: d}
		// A header the store could not read marks the entry unreadable; bytes
		// that are simply not an envelope do not (Header's own split), which is
		// what makes the viewer's unreadable row marker reachable for a listed
		// object whose bytes cannot be opened (go-cask#357).
		version, codec, typ, err := Header(ctx, source, d)
		if err != nil {
			e.Unreadable = true
		} else {
			e.Version, e.Codec, e.Type = version, codec, typ
		}
		if hasPhysical {
			size, written, err := physical.Stat(ctx, d)
			if err != nil {
				e.Unreadable = true
			} else {
				e.Size, e.Written = size, written
				s.Bytes += e.Size
			}
		} else {
			if e.Size, err = source.Size(ctx, d); err != nil {
				e.Unreadable = true
			} else {
				s.Bytes += e.Size
			}
			if e.Written, err = source.ModTime(ctx, d); err != nil {
				e.Unreadable = true
			}
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
