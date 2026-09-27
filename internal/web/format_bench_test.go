// Benchmarks for the presentation helpers in format.go. The Bytes tab renders
// one dump per inspector reveal, over a preview of at most previewLimit bytes
// (viewer-design §3), so the dump benchmark measures the largest dump the
// viewer produces — 16 rows.

package web

import (
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// benchmarkDumpPayload is a deterministic previewLimit-byte payload.
func benchmarkDumpPayload() []byte {
	data := make([]byte, previewLimit)
	for i := range data {
		data[i] = byte(i)
	}
	return data
}

// benchmarkRows keeps the dump observable so the call is not eliminated.
var benchmarkRows []dumpRow

// BenchmarkHexdump measures formatting one full Bytes-tab dump. Its allocations
// are the number performance §4 asks a hot path to bound: a small multiple of
// the 16 rows, never of the 256 bytes.
func BenchmarkHexdump(b *testing.B) {
	data := benchmarkDumpPayload()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		benchmarkRows = hexdump(data)
	}
	if len(benchmarkRows) != len(data)/16 {
		b.Fatalf("hexdump(%d bytes) = %d rows, want %d", len(data), len(benchmarkRows), len(data)/16)
	}
}

// benchmarkShort keeps the abbreviated digest observable.
var benchmarkShort string

// BenchmarkShortDigest measures one object row's abbreviated digest.
func BenchmarkShortDigest(b *testing.B) {
	digest := sha256.Of([]byte("one rendered object row"))
	b.ReportAllocs()
	for b.Loop() {
		benchmarkShort = shortDigest(digest)
	}
	if benchmarkShort == "" {
		b.Fatal("shortDigest rendered nothing")
	}
}
