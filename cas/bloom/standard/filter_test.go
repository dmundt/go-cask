package standard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
)

func TestFilterStandard(t *testing.T) {
	f, err := New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("hello world"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("standard filter should contain stored digest")
	}
	missing := cas.NewDigest([]byte("not present"))
	if f.Contains(missing) {
		t.Fatal("standard filter should reject unseen digest")
	}
}

func TestFilterCustomIndexHash(t *testing.T) {
	hash := func(data []byte, i int) uint64 {
		return uint64(len(data) + i)
	}
	f, err := NewFilter(Config{ExpectedItems: 256, FalsePositiveRate: 0.01, Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []cas.Digest{cas.NewDigest([]byte("custom")), cas.NewDigest([]byte("custom-2"))} {
		f.Add(d)
		if !f.Contains(d) {
			t.Fatal("custom hash filter should contain stored digest")
		}
	}
}

func TestFilterValidationAndReset(t *testing.T) {
	if _, err := New(0, 0.01); err == nil {
		t.Fatal("expected error for zero expected items")
	}
	if _, err := New(256, 0); err == nil {
		t.Fatal("expected error for invalid false positive rate")
	}
	if _, err := New(256, 1.0); err == nil {
		t.Fatal("expected error for invalid false positive rate")
	}

	f, err := New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	zero := cas.Digest{}
	if f.Contains(zero) {
		t.Fatal("zero digest should never be present")
	}
	f.Add(zero)
	if f.Contains(zero) {
		t.Fatal("zero digest should be ignored on Add")
	}

	d := cas.NewDigest([]byte("reset me"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("digest should be present after Add")
	}
	f.Reset()
	if f.Contains(d) {
		t.Fatal("Reset should clear all bits")
	}
}

func TestGuardUsesFilterForHotPath(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	filter, err := New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := bloom.NewGuard(backend, filter)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("hot path"))
	if err := guard.Put(ctx, d, io.NopCloser(bytes.NewReader([]byte("payload")))); err != nil {
		t.Fatal(err)
	}
	if !filter.Contains(d) {
		t.Fatal("guard should add digest to filter")
	}
	ok, err := guard.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("guard.Exists = (%v, %v), want (true, nil)", ok, err)
	}
}

func Example() {
	ctx := context.Background()
	backend := backmem.New()
	filter, err := New(10_000, 0.01)
	if err != nil {
		fmt.Println("filter error:", err)
		return
	}
	guard, err := bloom.NewGuard(backend, filter)
	if err != nil {
		fmt.Println("guard error:", err)
		return
	}

	d := cas.Digest("hello world")
	if err := guard.Put(ctx, d, strings.NewReader("hello world")); err != nil {
		fmt.Println("put error:", err)
		return
	}
	ok, err := guard.Exists(ctx, d)
	if err != nil {
		fmt.Println("exists error:", err)
		return
	}
	fmt.Println("exists:", ok)
	// Output:
	// exists: true
}
