package main

import (
	"hash"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// spoolAndHash copies r into w while hashing it, returning the byte count
// written. hasher receives exactly the bytes copied into w, so the digest is
// available from it afterwards (hasher.Sum(nil)). The algorithm is the client's
// choice (cas/hash/sha256 here); the core names none (cas-core §4.2).
func spoolAndHash(w io.Writer, hasher hash.Hash, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}

// envelopeType extracts the versioned type name from the self-describing TLV
// envelope header (cas-core §8 decision 1) on a best-effort basis; "" when the
// bytes are not an envelope (raw objects have no type).
//
// The header walk belongs to cas.EnvelopeType, which owns the layout and reads
// only the header: a bounded prefix of a large object still yields its type,
// where cas.EnvelopeFromBytes decodes the payload too and therefore needs the
// whole object.
func envelopeType(data []byte) string {
	name, err := cas.EnvelopeType(data)
	if err != nil {
		return ""
	}
	return name
}
