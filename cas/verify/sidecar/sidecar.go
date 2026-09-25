// Package sidecar keeps an optional per-object checksum beside a store's bytes.
//
// The object address stays the authoritative identity — a sidecar record never
// changes it and never becomes part of the hashed bytes. A record adds the one
// thing content addressing cannot express: a *cheap* second check over a store
// whose address is a strong hash, so an operator can re-read the bytes with a
// CRC while trusting SHA-256 for identity.
//
// The layout is a directory inside the backend's base:
//
//	<base>/<fan-out>/<hex>   object bytes (the backend's own layout, unchanged)
//	<base>/.meta/<hex>.json  sidecar record
//	<base>/.meta/*.tmp       atomic-write scratch, reclaimed by the backend
//
// Records are invisible to the backend's own List and Stats (the `.json` suffix
// is what keeps them out of a digest scan) and reclaimable scratch is removed by
// the backend's Clean. Deleting .meta loses no object: it removes only the cheap
// check. See docs/specs/operations.md §6 for the normative contract.
//
// A Backend decorates any cas.Backend, so it composes with everything that
// already accepts one (cas.New, gitlike.NewRepository, internal/store.Open):
//
//	rec, err := sidecar.New(backend,
//		sidecar.WithBase("store/objects"),
//		sidecar.WithChecksum(crc32.Name, crc32.New()),
//	)
//	store := cas.New(rec, json.New[*Blob](), sha256.New())
//
// The decorator is opt-in and off by default: nothing in the core, the CLI or
// the viewer writes a record unless a caller asks for one.
package sidecar

import (
	"errors"
	"fmt"

	"github.com/dmundt/go-cask/cas"
)

// ErrChecksumAlgorithm reports that a record was written with a different
// checksum algorithm than the reader was configured with. The stored bytes are
// untouched — the reader changed — so a wrong-algorithm read is deliberately
// kept distinct from corruption, the same way cas.ErrCodecMismatch is kept
// distinct from cas.ErrCorrupt. Width alone cannot tell the two apart: crc32
// and adler32 both produce four bytes.
var ErrChecksumAlgorithm = errors.New("sidecar: checksum algorithm mismatch")

// ErrUnrecorded reports that an object has no sidecar record. It wraps
// cas.ErrNotFound — the record is absent — and it is never corruption: an object
// without a record is simply unchecked, which is why the check reports absence
// rather than damage (the lesson of the deleted examples/files .crc32 sidecar,
// go-cask#196). A caller that only cares whether the record exists tests
// cas.ErrNotFound; one that must tell "no record" apart from "no object" tests
// this.
var ErrUnrecorded = fmt.Errorf("%w: sidecar: object has no checksum record", cas.ErrNotFound)

// ErrRecordTooLarge reports that a record cannot be published within the read cap
// the writer was configured with (WithMaxRecordBytes, DefaultMaxRecordBytes by
// default), so the write is refused and no record is written.
//
// The write path drops the optional type and codec fields before it gives up —
// both are derived best-effort and documented as never required — so this says
// the cap is too small for a record's own required fields (a digest, a checksum,
// a size and a creation time), not that one object is unusual.
//
// It exists so the writer and the reader cannot disagree silently: a record is
// either published in a shape readRecord accepts, or not published at all.
// Publishing one the reader would refuse as cas.ErrCorrupt is the one outcome the
// write path must never produce (go-cask#362).
var ErrRecordTooLarge = errors.New("sidecar: record exceeds the read cap")
