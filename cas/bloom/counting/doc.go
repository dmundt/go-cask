// Package counting provides a counting Bloom filter for CAS digests.
//
// The counting variant tracks multiplicity per bit so entries can be removed as
// the underlying set changes. It is useful when the filter is used to track
// mutable membership hints, but it remains advisory and cannot replace the real
// backend's authority over object identity.
package counting
