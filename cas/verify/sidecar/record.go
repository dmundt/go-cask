package sidecar

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// RecordVersion is the record layout this build writes. A reader refuses any
// other version with cas.ErrCorrupt instead of guessing at a layout it does not
// know.
const RecordVersion = 1

// Record is the sidecar record for one object, stored as JSON at
// <base>/.meta/<hex>.json. It describes stored bytes that are addressed by the
// caller's strong hash; the recorded checksum is a cheap second signal over the
// same bytes, never part of the object's identity.
type Record struct {
	// Version is the record layout version (RecordVersion for this build).
	Version int `json:"version"`
	// Digest is the object's address. It is a cas.Digest, so it renders as one
	// lowercase-hex string through encoding.TextMarshaler.
	Digest cas.Digest `json:"digest"`
	// Type is the envelope's object type ("blob@1"), or "" when the stored
	// bytes are not a go-cask envelope. It is derived from a bounded prefix and
	// is never required.
	Type string `json:"type,omitempty"`
	// Codec is the envelope's codec identity tag ("json"), or "" for the same
	// reason as Type.
	Codec string `json:"codec,omitempty"`
	// ChecksumAlgo names the algorithm the writer computed with (for example
	// crc32.Name). A reader compares it, because crc32 and adler32 are both
	// four bytes wide: width alone cannot tell a wrong-algorithm read from
	// corruption.
	ChecksumAlgo string `json:"checksum_algo"`
	// Checksum is the checksum of the stored bytes, computed in one pass over
	// exactly what the backend received (and therefore what Get returns).
	Checksum cas.Digest `json:"checksum"`
	// Size is the stored byte count. A disagreement with the object's actual
	// size is reported as corruption, never repaired.
	Size int64 `json:"size"`
	// CreatedAt is when the record was first written (UTC). A repeat Put of the
	// same digest under the same algorithm leaves an existing valid record
	// untouched, so a record stays deterministic and the object model stays
	// immutable.
	CreatedAt time.Time `json:"created_at"`
}

// validate reports whether every required field is present and usable. It is
// the read path's guard: a record that fails it is cas.ErrCorrupt, never a
// silent skip.
func (r *Record) validate() error {
	if r.Version != RecordVersion {
		return fmt.Errorf("record version %d is not %d", r.Version, RecordVersion)
	}
	if r.Digest.IsZero() {
		return fmt.Errorf("record has no digest")
	}
	if r.ChecksumAlgo == "" {
		return fmt.Errorf("record has no checksum algorithm")
	}
	if r.Checksum.IsZero() {
		return fmt.Errorf("record has no checksum")
	}
	if r.Size < 0 {
		return fmt.Errorf("record has negative size %d", r.Size)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("record has no creation time")
	}
	return nil
}

// encode renders the record as the JSON stored on disk.
func (r *Record) encode() ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode record: %w", err)
	}
	return b, nil
}

// decodeRecord parses and validates a stored record. Every failure is
// cas.ErrCorrupt: the bytes are the store's own metadata, so an unreadable
// record is damage rather than a reader-side change.
func decodeRecord(b []byte) (*Record, error) {
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("%w: sidecar: decode record: %v", cas.ErrCorrupt, err)
	}
	if err := rec.validate(); err != nil {
		return nil, fmt.Errorf("%w: sidecar: %v", cas.ErrCorrupt, err)
	}
	return &rec, nil
}
