package sha512_test

import (
	"errors"
	"strings"
	"testing"

	sha512hash "github.com/dmundt/go-cask/cas/hash/sha512"
)

// brokenReader fails after the first byte, so a streaming digest must report
// the read failure instead of hashing a truncated stream.
type brokenReader struct {
	boom error
	read bool
}

func (r *brokenReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.boom
	}
	r.read = true
	p[0] = 'x'
	return 1, nil
}

// TestDigestReportsAReadFailure pins the streaming error branch: a reader that
// fails mid-stream is reported as an error naming the algorithm, and no digest
// is returned for the partial stream.
func TestDigestReportsAReadFailure(t *testing.T) {
	boom := errors.New("read exploded")
	d, err := sha512hash.New().Digest(&brokenReader{boom: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("Digest with a failing reader = %v, want %v", err, boom)
	}
	if !strings.Contains(err.Error(), "cas/sha512") {
		t.Fatalf("Digest with a failing reader = %v, want it to name the algorithm", err)
	}
	if d != nil {
		t.Fatalf("Digest with a failing reader = %s, want no digest", d)
	}
}
