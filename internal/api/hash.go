package api

import (
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash"

	"github.com/dmundt/go-cask/cas"
)

// newHasherFor returns a streaming hasher for a registered algorithm (the
// built-ins sha256/sha1; anything else is unknown, R-01).
func newHasherFor(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha1":
		return sha1.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", cas.ErrUnknownAlgorithm, algo)
	}
}
