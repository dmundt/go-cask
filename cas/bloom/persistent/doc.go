// Package persistent provides a Bloom filter whose bitset is kept in a file so
// advisory negative lookups survive process restarts.
//
// The persistent variant keeps the same probabilistic semantics as the other
// Bloom filters but stores bits on disk so the hint set can be reused without a
// full rebuild. It is still not the source of truth; the authority remains the
// backend and the caller-owned CAS hasher/digest model.
//
// How the file is used is platform dependent and reported by Filter.IsMapped: on
// platforms with memory mapping the bitset is a live shared view of the file,
// while a platform without one (Windows today) reads the file into a heap
// buffer and writes it back from Sync or Close. A Filter MUST NOT be used after
// Close; see Filter's documentation for the full lifetime contract.
package persistent
