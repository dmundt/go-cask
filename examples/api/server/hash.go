package main

import (
	"encoding/binary"
	"io"
	"strings"
)

// spoolAndHash copies r into w while hashing it, returning the byte count.
// The digest is available from the hasher after the copy. The algorithm is the
// client's choice (cas/hash/sha256 here); the core names none (cas-core §4.2).
func spoolAndHash(w io.Writer, hasher interface {
	Write([]byte) (int, error)
}, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}

// envelopeType extracts the versioned type name from the self-describing TLV
// envelope header (cas-core §8 decision 1) on a best-effort basis; "" when the
// bytes are not an envelope (raw objects have no type).
//
// Only the header — [version][uvarint typeLen][type] — is parsed, so a bounded
// prefix of a large object still yields its type (cas.EnvelopeFromBytes
// decodes the payload too and therefore needs the whole object).
func envelopeType(data []byte) string {
	const envelopeVersion = 1
	if len(data) < 1 || data[0] != envelopeVersion {
		return ""
	}
	typeLen, n := binary.Uvarint(data[1:])
	if n <= 0 {
		return ""
	}
	off := 1 + n
	if typeLen == 0 || typeLen > uint64(len(data)-off) {
		return ""
	}
	name := string(data[off : off+int(typeLen)])
	if !strings.Contains(name, "@") {
		name += "@1" // legacy unversioned type name
	}
	return name
}
