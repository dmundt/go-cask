// Package persistent provides a file-backed Bloom filter for advisory negative
// lookups across process restarts.
//
// The persistent variant keeps the same probabilistic semantics as the other
// Bloom filters but stores bits on disk so the hint set can be reused without a
// full rebuild. It is still not the source of truth; the authority remains the
// backend and the caller-owned CAS hasher/digest model.
package persistent
