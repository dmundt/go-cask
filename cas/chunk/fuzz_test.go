package chunk_test

import (
	"bytes"
	"testing"

	"github.com/dmundt/go-cask/cas/chunk"
)

func FuzzSplitJoinRoundTrip(f *testing.F) {
	f.Add([]byte("hello"), 3)
	f.Add([]byte("abcdefghijklmnopqrstuvwxyz"), 7)
	f.Add([]byte{}, 1)
	f.Add([]byte("abc123xyz"), -4)

	f.Fuzz(func(t *testing.T, in []byte, size int) {
		if size < 0 {
			size = -size
		}
		got := chunk.Join(chunk.Split(in, size))
		if !bytes.Equal(got, in) {
			t.Fatalf("Join(Split(%q, %d)) = %q, want %q", in, size, got, in)
		}
	})
}
