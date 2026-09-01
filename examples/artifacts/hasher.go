package main

import (
	"crypto/sha256"

	"github.com/dmundt/go-cask/cas"
)

// Register the custom "sha256double" algorithm: hash the content twice.
// This is a deliberately trivial std-lib-only custom algorithm demonstrating
// cas.RegisterHash (cas-core §7.2) — the algorithm name travels with every
// address, so a store using it can mix with sha1/sha256 objects freely.
// The name obeys the hash-string validation pattern (lowercase alnum, per
// defaults §2); the illustrative "sha256-double" of the examples spec is
// not a valid algorithm name.
func init() {
	cas.RegisterHash("sha256double", func(data []byte) cas.Hash {
		first := sha256.Sum256(data)
		second := sha256.Sum256(first[:])
		h, err := cas.NewHash("sha256double", second[:])
		if err != nil {
			panic(err) // registered above; cannot fail
		}
		return h
	})
}
