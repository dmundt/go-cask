package chunk_test

import (
	"bytes"
	"testing"

	"github.com/dmundt/go-cask/cas/chunk"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	want := bytes.Repeat([]byte("abc123"), 19)
	parts := chunk.Split(want, 7)
	if len(parts) == 0 {
		t.Fatal("Split returned no chunks")
	}
	got := chunk.Join(parts)
	if !bytes.Equal(got, want) {
		t.Fatalf("Join(Split(x)) mismatch: got %q, want %q", got, want)
	}
}

func TestCountAndZero(t *testing.T) {
	if got := chunk.Count(0, 16); got != 0 {
		t.Fatalf("Count(0,16) = %d, want 0", got)
	}
	if got := chunk.Count(10, 0); got != 1 {
		t.Fatalf("Count(10,0) = %d, want 1", got)
	}
	if got := chunk.Split(nil, 8); len(got) != 0 {
		t.Fatalf("Split(nil) = %#v, want empty slice", got)
	}
}
