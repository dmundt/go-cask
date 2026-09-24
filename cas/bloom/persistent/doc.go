// Package persistent provides a Bloom filter whose bitset is kept in a file so
// advisory negative lookups survive process restarts.
//
// The persistent variant keeps the same probabilistic semantics as the other
// Bloom filters but stores bits on disk so the hint set can be reused without a
// full rebuild. It is still not the source of truth; the authority remains the
// backend and the caller-owned CAS hasher/digest model.
//
// The file is a fixed header followed by the bitset. The header carries the
// 32-byte index key the default index hash is derived from, which is what makes
// the default restart-stable: a hash seeded per process would report every
// digest recorded by another process as absent, and bloom.Guard treats a
// negative as authoritative (header.go, go-cask#254). A caller-supplied
// Config.Hash must be deterministic for the same reason, and the header records
// which kind wrote the file, so a file written under the other kind is rebuilt
// rather than trusted.
//
// How the file is used is platform dependent and reported by Filter.IsMapped: on
// platforms with memory mapping the bitset is a live shared view of the file,
// while a platform without one (Windows today) reads the file into a heap
// buffer and writes it back from Sync or Close. A Filter MUST NOT be used after
// Close; see Filter's documentation for the full lifetime contract.
package persistent
