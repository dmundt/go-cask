package main

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// newHasher returns a streaming hasher for a registered algorithm (the
// built-ins sha256/sha1; anything else is unknown, R-01).
func newHasher(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha1":
		return sha1.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", cas.ErrUnknownAlgorithm, algo)
	}
}

// hashBytes hashes data with a registered algorithm.
func hashBytes(algo string, data []byte) (cas.Hash, error) {
	h, err := newHasher(algo)
	if err != nil {
		return nil, err
	}
	h.Write(data)
	return cas.NewHash(algo, h.Sum(nil))
}

// spoolAndHash copies r into w while hashing it, returning the byte count.
// The hash is available from the hasher after the copy.
func spoolAndHash(w io.Writer, hasher hash.Hash, r io.Reader) (int64, error) {
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
