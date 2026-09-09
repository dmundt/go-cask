// Package index provides the listing/metadata helpers shared by the cask CLI
// and the viewer: pagination over stored hashes and best-effort type
// detection from the self-describing TLV envelope (cas-core §8 decision 1).
package index

import "github.com/dmundt/go-cask/cas"

// Paginate returns the items in the [offset, offset+limit) window,
// bounded to the slice (list pagination per defaults §3).
func Paginate[T any](items []T, offset, limit int) []T {
	off := max(offset, 0)
	lo := min(off, len(items))
	hi := min(max(off+limit, lo), len(items))
	return items[lo:hi]
}

// EnvelopeType extracts the versioned type name ("blob@1", …) from the
// self-describing TLV envelope (cas-core §8 decision 1) on a best-effort
// basis; "" when the bytes are not an envelope (raw objects have no type).
// A legacy unversioned type name reads back with "@1" appended (object-
// versioning §2).
func EnvelopeType(data []byte) string {
	env, err := cas.EnvelopeFromBytes(data)
	if err != nil {
		return ""
	}
	return env.Type
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
