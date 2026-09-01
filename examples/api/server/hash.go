package main

import (
	"encoding/json"
	"io"
)

// spoolAndHash copies r into w while hashing it, returning the byte count.
// The hash is available from the hasher after the copy. Hashing itself uses
// cas.NewHasher / cas.HashBytes (cas-core §4.2).
func spoolAndHash(w io.Writer, hasher interface {
	Write([]byte) (int, error)
}, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}

// envelopeType extracts the versioned type name from the self-describing
// envelope (cas-core §8 decision 1) on a best-effort basis; "" when the
// bytes are not an envelope (raw objects have no type).
func envelopeType(data []byte) string {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil || env.Type == "" {
		return ""
	}
	return env.Type
}
