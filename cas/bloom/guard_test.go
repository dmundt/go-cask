package bloom_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
	stdfilter "github.com/dmundt/go-cask/cas/bloom/standard"
)

func TestGuardPutAddsDigestToFilter(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	filter, err := stdfilter.New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}

	guard := bloom.NewGuard(raw, filter)
	d := cas.NewDigest([]byte("guard put"))
	if err := guard.Put(ctx, d, io.NopCloser(bytes.NewReader([]byte("payload")))); err != nil {
		t.Fatal(err)
	}
	if !filter.Contains(d) {
		t.Fatal("guard should add inserted digest to its filter")
	}
	ok, err := guard.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("guard.Exists = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestGuardExistsShortCircuitsOnNegativeBloomResult(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	filter, err := stdfilter.New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}

	guard := bloom.NewGuard(raw, filter)
	d := cas.NewDigest([]byte("absent"))

	ok, err := guard.Exists(ctx, d)
	if err != nil {
		t.Fatalf("guard.Exists returned unexpected error: %v", err)
	}
	if ok {
		t.Fatal("guard.Exists should reject a digest not present in the Bloom filter")
	}
}

type countingLikeFilter struct {
	present map[string]bool
}

func (f *countingLikeFilter) Add(d cas.Digest) {
	f.present[d.String()] = true
}

func (f *countingLikeFilter) Contains(d cas.Digest) bool {
	return f.present[d.String()]
}

func (f *countingLikeFilter) Remove(d cas.Digest) {
	delete(f.present, d.String())
}

func TestGuardDeleteRemovesFromFilterWhenSupported(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	filter := &countingLikeFilter{present: map[string]bool{}}
	guard := bloom.NewGuard(raw, filter)
	d := cas.NewDigest([]byte("delete me"))
	if err := guard.Put(ctx, d, io.NopCloser(bytes.NewReader([]byte("payload")))); err != nil {
		t.Fatal(err)
	}
	if !filter.Contains(d) {
		t.Fatal("guard should record digest before delete")
	}
	if err := guard.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if filter.Contains(d) {
		t.Fatal("guard.Delete should remove the digest from a removable filter")
	}
}
