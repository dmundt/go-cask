package api

import "io"

// spoolAndHash copies r into w while hashing it, returning the byte count.
// The hash is available from the hasher after the copy (hash-on-write,
// R-01). Hashing itself uses cas.NewHasher / cas.HashBytes (cas-core §4.2).
func spoolAndHash(w io.Writer, hasher interface {
	Write([]byte) (int, error)
}, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}
