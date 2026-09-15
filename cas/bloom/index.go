package bloom

import (
	"encoding/binary"
	"hash/maphash"
)

// IndexHash turns digest bytes plus a probe index into a stable Bloom bit
// position. It is a Bloom-specific indexing detail and not part of the CAS
// object hash or the core `cas.Digest` contract.
type IndexHash func(data []byte, i int) uint64

var defaultIndexSeed = maphash.MakeSeed()

// DefaultIndexHash uses a process-local map hash with a probe-dependent suffix.
// This keeps the default path fast and does not depend on a client-supplied
// hasher, but callers that need cross-process or restart-stable Bloom indexes
// should override it with their own deterministic IndexHash.
func DefaultIndexHash(data []byte, i int) uint64 {
	var h maphash.Hash
	h.SetSeed(defaultIndexSeed)
	_, _ = h.Write(data)
	var probe [8]byte
	binary.LittleEndian.PutUint64(probe[:], uint64(i))
	_, _ = h.Write(probe[:])
	return h.Sum64()
}
