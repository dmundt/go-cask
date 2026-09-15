// Package bloom provides optional advisory Bloom-filter helpers for CAS digests.
//
// Bloom filters are a probabilistic fast-path for negative lookups: a missing
// digest can be rejected before an expensive backend check, while a positive hit
// remains only a hint and must still be verified against the real store.
//
// The package is intentionally not part of the authoritative CAS identity model.
// The client-owned hasher and the underlying backend remain the source of truth
// for object identity and integrity. This package is a compatibility layer that
// sits in front of a backend or store to accelerate hot-path absence checks.
//
// The root package defines shared helpers and the advisory Guard wrapper. The
// actual filter implementations live in subpackages so callers can choose a
// memory-only, counting, or persistent variant without changing the semantics of
// the underlying store.
package bloom
