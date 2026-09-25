package main

import (
	"hash"
	"io"
)

// spoolAndHash copies r into w while hashing it, returning the byte count
// written. hasher receives exactly the bytes copied into w, so the digest is
// available from it afterwards (hasher.Sum(nil)). The algorithm is the client's
// choice (cas/hash/sha256 here); the core names none (cas-core §4.2).
func spoolAndHash(w io.Writer, hasher hash.Hash, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}
