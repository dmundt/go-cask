// Package standard provides an in-memory Bloom filter for advisory negative
// lookups against CAS digests.
//
// It is the lowest-overhead bloom option for hot paths where the working set is
// small enough to keep in memory. A positive result is only a hint; the real
// backend remains the source of truth.
package standard
