package main

import (
	"fmt"

	"github.com/dmundt/go-cask/cas"
	hashutil "github.com/dmundt/go-cask/cas/hash"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
)

// hashAlgoUsage documents -hash-algo's accepted values, so the commands that
// take it cannot drift apart.
const hashAlgoUsage = "digest algorithm: sha256, sha512, or sha512_256"

// digestAlgorithm is one selectable -hash-algo value: the hasher the CLI
// injects, plus the name and width its printable digest form needs.
//
// The cas core names no algorithm — a Digest is just bytes and the client
// injects the Hasher — so this mapping from a flag value to an algorithm lives
// in the CLI, at the one place that has to turn a flag into one (cli.md §1,
// §2). `web`, `seed-preview` and `verify` take -hash-algo; a store addressed by
// another algorithm is inspected through them.
type digestAlgorithm struct {
	// name is the algorithm name used in the printable digest form.
	name string
	// size is the digest width in bytes.
	size int
	// hasher computes and validates a digest of this algorithm.
	hasher cas.Hasher
}

// Parse accepts the printable "<name>:hexdigest" form and the bare hex form of
// this algorithm, and rejects anything else (including another algorithm's
// prefix) with cas.ErrInvalidDigest.
func (a digestAlgorithm) Parse(s string) (cas.Digest, error) {
	return hashutil.ParseDigest(a.name, s, a.size)
}

// Format renders a digest in the printable "<name>:hexdigest" form, and the
// absent digest as "".
func (a digestAlgorithm) Format(d cas.Digest) string {
	return hashutil.FormatDigest(a.name, d)
}

// hashAlgorithms lists the shipped algorithms -hash-algo accepts, in the order
// the usage text names them.
var hashAlgorithms = []digestAlgorithm{
	{name: sha256.Name, size: sha256.Size, hasher: sha256.New()},
	{name: sha512.Name, size: sha512.Size, hasher: sha512.New()},
	{name: sha512256.Name, size: sha512256.Size, hasher: sha512256.New()},
}

// lookupDigestAlgorithm resolves a -hash-algo value. An unknown name is a
// caller error, so the commands report it as a usage error (cli.md §3).
func lookupDigestAlgorithm(name string) (digestAlgorithm, error) {
	for _, a := range hashAlgorithms {
		if a.name == name {
			return a, nil
		}
	}
	return digestAlgorithm{}, fmt.Errorf("unknown digest algorithm %q (want %q, %q, or %q)",
		name, sha256.Name, sha512.Name, sha512256.Name)
}
