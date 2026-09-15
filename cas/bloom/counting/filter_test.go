package counting

import (
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestFilterCounting(t *testing.T) {
	f, err := New(256, 0.01, 8)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("counter test"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("counting filter should contain stored digest")
	}
	f.Remove(d)
	if f.Contains(d) {
		t.Fatal("counting filter should reject digest after removal")
	}
}

func TestFilterCountingValidationAndReset(t *testing.T) {
	if _, err := New(0, 0.01, 8); err == nil {
		t.Fatal("expected error for zero expected items")
	}
	if _, err := New(256, 0, 8); err == nil {
		t.Fatal("expected error for invalid false positive rate")
	}
	if _, err := New(256, 0.01, 7); err == nil {
		t.Fatal("expected error for unsupported counter bit width")
	}

	f, err := NewFilter(Config{ExpectedItems: 256, FalsePositiveRate: 0.01, CounterBits: 4, Hash: func(data []byte, i int) uint64 { return uint64(len(data) + i) }})
	if err != nil {
		t.Fatal(err)
	}
	if f.Contains(cas.Digest{}) {
		t.Fatal("zero digest should never be present")
	}
	f.Add(cas.Digest{})
	f.Remove(cas.Digest{})

	d := cas.NewDigest([]byte("counting reset"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("digest should be present after Add")
	}
	f.Reset()
	if f.Contains(d) {
		t.Fatal("Reset should clear all counters")
	}
}
