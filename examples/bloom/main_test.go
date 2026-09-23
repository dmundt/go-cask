package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
	stdfilter "github.com/dmundt/go-cask/cas/bloom/standard"
)

func TestBloomExample(t *testing.T) {
	ctx := context.Background()
	backend := mem.New()
	custom := func(data []byte, i int) uint64 {
		var h uint64
		for _, b := range data {
			h = h*131 + uint64(b)
		}
		return h + uint64(i*17)
	}
	filter, err := stdfilter.NewFilter(stdfilter.Config{
		ExpectedItems:     1024,
		FalsePositiveRate: 0.01,
		Hash:              custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := bloom.NewGuard(backend, filter)
	if err != nil {
		t.Fatal(err)
	}

	d := cas.NewDigest([]byte("hello world"))
	if err := guard.Put(ctx, d, bytes.NewReader([]byte("hello world"))); err != nil {
		t.Fatal(err)
	}
	if !filter.Contains(d) {
		t.Fatal("filter should contain stored digest")
	}
	if ok, err := guard.Exists(ctx, d); err != nil || !ok {
		t.Fatalf("guard.Exists(%s) = (%v, %v), want (true, nil)", d, ok, err)
	}
	missing := cas.NewDigest([]byte("not here"))
	if ok, err := guard.Exists(ctx, missing); err != nil || ok {
		t.Fatalf("guard.Exists(%s) = (%v, %v), want (false, nil)", missing, ok, err)
	}
}

func TestBloomDemo(t *testing.T) {
	if err := demo(); err != nil {
		t.Fatalf("demo() = %v, want nil", err)
	}
}
