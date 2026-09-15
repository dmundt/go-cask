// Command bloom demonstrates the optional Bloom layer: it wraps a backend with a
// probabilistic pre-check without changing the core digest/hasher contract.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
	stdfilter "github.com/dmundt/go-cask/cas/bloom/standard"
)

func customIndexHash(data []byte, i int) uint64 {
	// Standard double hashing: compute two base hashes and derive the next bit
	// position from them. This is a Bloom indexing detail, not the CAS object hash.
	var h1, h2 uint64
	for _, b := range data {
		h1 = h1*131 + uint64(b)
		h2 = h2*137 + uint64(b) + 1
	}
	return h1 + uint64(i)*h2
}

func demo() error {
	ctx := context.Background()
	raw := mem.New()

	// We keep the object hash algorithm independent from the Bloom index. The CAS
	// object identity is still the digest bytes returned by the store's hasher.
	filter, err := stdfilter.NewFilter(stdfilter.Config{
		ExpectedItems:     10_000,
		FalsePositiveRate: 0.01,
		Hash:              customIndexHash,
	})
	if err != nil {
		return err
	}
	guard := bloom.NewGuard(raw, filter)

	stored := cas.NewDigest([]byte("hello world"))
	if err := guard.Put(ctx, stored, bytes.NewReader([]byte("hello world"))); err != nil {
		return err
	}

	// A positive Bloom result is only a hint.
	fmt.Printf("stored digest likely present: %v\n", filter.Contains(stored))
	ok, err := guard.Exists(ctx, stored)
	if err != nil {
		return err
	}
	fmt.Printf("backend confirms stored digest: %v\n", ok)

	missing := cas.NewDigest([]byte("never stored"))
	miss, err := guard.Exists(ctx, missing)
	if err != nil {
		return err
	}
	fmt.Printf("missing digest is absent: %v\n", miss)

	// Custom index hashes are useful when a caller wants a different scatter
	// pattern for the Bloom bitmap without changing the CAS digest/hasher model.
	fmt.Printf("custom index hash used: %v\n", filter.Contains(stored))
	return nil
}

func main() {
	if err := demo(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
