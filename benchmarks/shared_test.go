package benchmark_test

// Benchmarks per performance §5: every case reports allocations and bytes.
// Store-level benchmarks run against the mem backend (deterministic, no disk
// noise); the FSBackend cases cover disk behavior separately.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/dmundt/go-cask/cas"
	binarycodec "github.com/dmundt/go-cask/cas/codec/binary"
	cborcodec "github.com/dmundt/go-cask/cas/codec/cbor"
	flatecodec "github.com/dmundt/go-cask/cas/codec/flate"
	gobcodec "github.com/dmundt/go-cask/cas/codec/gob"
	gzipcodec "github.com/dmundt/go-cask/cas/codec/gzip"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	zlibcodec "github.com/dmundt/go-cask/cas/codec/zlib"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512_256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
)

// testNote is a small Object[T] used to size store benchmarks.
type testNote struct {
	Title string
	Body  string
}

func (testNote) Type() string             { return "note@1" }
func (testNote) References() []cas.Digest { return nil }

// digestData computes the content address of data with the client's hasher.
func digestData(data []byte) cas.Digest {
	return sha256.Of(data)
}

func benchText(size int, seed int) string {
	if size <= 0 {
		return ""
	}
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte((seed + i*17) % 251)
		if buf[i] == 0 {
			buf[i] = 'x'
		}
	}
	return string(buf)
}

func benchNoteWithSeed(size int, seed int) testNote {
	if size <= 0 {
		return testNote{}
	}
	left := size / 2
	return testNote{
		Title: benchText(left, seed),
		Body:  benchText(size-left, seed+1),
	}
}

func benchTitle(prefix string, n int) string {
	return prefix + strconv.Itoa(n)
}

func benchTitleUint(prefix string, n uint64) string {
	return prefix + strconv.FormatUint(n, 10)
}

func marshalBinaryNote(v testNote) ([]byte, error) {
	var buf bytes.Buffer
	if err := buf.WriteByte(1); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(v.Title))); err != nil {
		return nil, err
	}
	if _, err := buf.WriteString(v.Title); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(v.Body))); err != nil {
		return nil, err
	}
	if _, err := buf.WriteString(v.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unmarshalBinaryNote(data []byte) (testNote, error) {
	if len(data) == 0 {
		return testNote{}, fmt.Errorf("binary note: missing version")
	}
	if data[0] != 1 {
		return testNote{}, fmt.Errorf("binary note: unsupported version %d", data[0])
	}
	buf := bytes.NewReader(data[1:])
	var titleLen uint32
	if err := binary.Read(buf, binary.BigEndian, &titleLen); err != nil {
		return testNote{}, err
	}
	title := make([]byte, titleLen)
	if _, err := buf.Read(title); err != nil {
		return testNote{}, err
	}
	var bodyLen uint32
	if err := binary.Read(buf, binary.BigEndian, &bodyLen); err != nil {
		return testNote{}, err
	}
	body := make([]byte, bodyLen)
	if _, err := buf.Read(body); err != nil {
		return testNote{}, err
	}
	return testNote{Title: string(title), Body: string(body)}, nil
}

func marshalCBORNote(v testNote) ([]byte, error) {
	return cborcodec.NewMap().Encode(map[string]any{
		"title": v.Title,
		"body":  v.Body,
	})
}

func unmarshalCBORNote(data []byte) (testNote, error) {
	m, err := cborcodec.NewMap().Decode(data)
	if err != nil {
		return testNote{}, err
	}
	return testNote{
		Title: m["title"].(string),
		Body:  m["body"].(string),
	}, nil
}

// Keep the canonical size ladder intentionally small but log-spaced so it covers
// the real crossover bands without turning the suite into a broad matrix.
var benchSizes = []struct {
	name string
	size int
}{
	{"64B", 64},
	{"256B", 256},
	{"1KiB", 1024},
	{"4KiB", 4 * 1024},
	{"16KiB", 16 * 1024},
	{"64KiB", 64 * 1024},
	{"256KiB", 256 * 1024},
	{"1MiB", 1024 * 1024},
}

const (
	benchmarkWarmupOps = 8
	// benchmarkHotSetSize keeps the hot set intentionally small so the benchmark
	// models a realistic working set while still exercising read locality.
	benchmarkHotSetSize = 16
	// benchmarkMixedColdRatio means ~1 in 10 operations is a cold miss/new write,
	// which matches a mixed hot/cold access pattern without dominating the run.
	benchmarkMixedColdRatio = 10
)

func benchmarkWarmup(fn func()) {
	for i := 0; i < benchmarkWarmupOps; i++ {
		fn()
	}
}

func benchmarkSummary(b *testing.B, label string, payloadBytes int) {
	b.Helper()
	if os.Getenv("CASK_BENCH_SUMMARY") == "" || b.N <= 0 {
		return
	}
	b.StopTimer()
	elapsed := b.Elapsed()
	avgNsPerOp := float64(elapsed) / float64(b.N)
	if payloadBytes > 0 {
		throughputMBps := float64(payloadBytes) * float64(b.N) / (1024 * 1024) / elapsed.Seconds()
		b.Logf("%s: payload=%dB ops=%d avg_ns/op=%.2f throughput=%.2f MB/s elapsed=%s", label, payloadBytes, b.N, avgNsPerOp, throughputMBps, elapsed)
		return
	}
	b.Logf("%s: ops=%d avg_ns/op=%.2f elapsed=%s", label, b.N, avgNsPerOp, elapsed)
}

var benchCodecs = []struct {
	name string
	new  func() cas.Codec[testNote]
}{
	{name: "json", new: func() cas.Codec[testNote] { return jsoncodec.New[testNote]() }},
	{name: "gzip", new: func() cas.Codec[testNote] { return gzipcodec.New(jsoncodec.New[testNote]()) }},
	{name: "zlib", new: func() cas.Codec[testNote] { return zlibcodec.New(jsoncodec.New[testNote]()) }},
	{name: "flate", new: func() cas.Codec[testNote] { return flatecodec.New(jsoncodec.New[testNote]()) }},
	{name: "gob", new: func() cas.Codec[testNote] { return gobcodec.New[testNote]() }},
	{name: "binary", new: func() cas.Codec[testNote] {
		return binarycodec.NewRaw(marshalBinaryNote, unmarshalBinaryNote)
	}},
	{name: "cbor", new: func() cas.Codec[testNote] {
		return cborcodec.NewRaw(marshalCBORNote, unmarshalCBORNote)
	}},
}

var benchHashers = []struct {
	name string
	new  func() cas.Hasher
}{
	{name: "sha256", new: func() cas.Hasher { return sha256.New() }},
	{name: "sha512", new: func() cas.Hasher { return sha512.New() }},
	{name: "sha512_256", new: func() cas.Hasher { return sha512_256.New() }},
}
