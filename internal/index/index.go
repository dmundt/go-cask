// Package index provides the listing/metadata helpers shared by the cask CLI
// and the viewer: pagination over stored digests and best-effort type
// detection from the self-describing TLV envelope (cas-core §8 decision 1).
package index

import (
	"encoding/binary"
	"errors"
	"strings"
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

// envelopeVersion mirrors the current TLV envelope version (cas-core §8
// decision 1, cas.envelopeVersion).
const envelopeVersion byte = 1

// errNotEnvelope reports bytes that do not start with a valid envelope header.
var errNotEnvelope = errors.New("index: not an envelope header")

// EnvelopeType extracts the versioned type name ("blob@1", …) from the
// self-describing TLV envelope (cas-core §8 decision 1) on a best-effort
// basis; "" when the bytes are not an envelope (raw objects have no type).
// A legacy unversioned type name reads back with "@1" appended (object-
// versioning §2).
//
// Only the header — [version][uvarint typeLen][type] — is inspected, so a
// truncated object prefix (the viewer reads a bounded prefix, not the whole
// object) still yields its type. This mirrors the header logic of the
// unexported cas parser; keep the two in step.
func EnvelopeType(data []byte) string {
	typ, err := envelopeType(data)
	if err != nil {
		return ""
	}
	return typ
}

func envelopeType(data []byte) (string, error) {
	if len(data) < 1 || data[0] != envelopeVersion {
		return "", errNotEnvelope
	}
	typeLen, n := binary.Uvarint(data[1:])
	if n <= 0 {
		return "", errNotEnvelope
	}
	off := 1 + n
	if typeLen == 0 || typeLen > uint64(len(data)-off) {
		return "", errNotEnvelope
	}
	name := string(data[off : off+int(typeLen)])
	if !strings.Contains(name, "@") {
		name += "@1" // legacy unversioned type name
	}
	return name, nil
}
