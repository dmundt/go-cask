// Package counting provides a counting Bloom filter for CAS digests.
//
// The counting variant tracks multiplicity per bit so entries can be removed as
// the underlying set changes. It is useful when the filter is used to track
// mutable membership hints, but it remains advisory and cannot replace the real
// backend's authority over object identity.
//
// Config.CounterBits selects the width of each packed counter — 4, 8 or 16 bits,
// so the backing array is m*CounterBits/8 bytes — and a counter saturates at its
// width's maximum. A configuration whose packed counters would need more than
// MaxCounterBytes (2 GiB) is refused with ErrFilterTooLarge rather than
// allocated (go-cask#383).
package counting
