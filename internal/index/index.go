// Package index provides the listing/metadata helpers shared by the CAS API
// and (later) the viewer: pagination over stored hashes and best-effort
// type detection from the self-describing envelope (cas-core §8 decision 1).
package index

import "encoding/json"

// Paginate returns the items in the [offset, offset+limit) window,
// bounded to the slice (list pagination per cas-api R-05).
func Paginate[T any](items []T, offset, limit int) []T {
	off := max(offset, 0)
	lo := min(off, len(items))
	hi := min(max(off+limit, lo), len(items))
	return items[lo:hi]
}

// EnvelopeType extracts the versioned type name ("blob@1", …) from the
// self-describing envelope on a best-effort basis; "" when the bytes are
// not an envelope (raw objects have no type).
func EnvelopeType(data []byte) string {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil || env.Type == "" {
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
