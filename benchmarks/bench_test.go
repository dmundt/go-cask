package benchmark_test

// Benchmarks per performance §5: every case reports allocations and bytes.
// Store-level benchmarks run against the mem backend (deterministic, no disk
// noise); the FSBackend cases cover disk behavior separately.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	binarycodec "github.com/dmundt/go-cask/cas/codec/binary"
	gobcodec "github.com/dmundt/go-cask/cas/codec/gob"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
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

func benchNote(size int) testNote {
	return testNote{Title: strings.Repeat("a", size/2), Body: strings.Repeat("b", size/2)}
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

var benchSizes = []struct {
	name string
	size int
}{
	{"64B", 64},
	{"256B", 256},
	{"1KiB", 1024},
	{"8KiB", 8 * 1024},
	{"64KiB", 64 * 1024},
	{"1MiB", 1024 * 1024},
}

var backendBenchSizes = []struct {
	name string
	size int
}{
	{"64B", 64},
	{"256B", 256},
	{"1KiB", 1024},
	{"8KiB", 8 * 1024},
	{"64KiB", 64 * 1024},
	{"256KiB", 256 * 1024},
	{"1MiB", 1024 * 1024},
}

func benchmarkSummary(b *testing.B, label string, payloadBytes int) {
	b.Helper()
	if b.N <= 0 {
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
	{name: "gob", new: func() cas.Codec[testNote] { return gobcodec.New[testNote]() }},
	{name: "binary", new: func() cas.Codec[testNote] {
		return binarycodec.New(marshalBinaryNote, unmarshalBinaryNote)
	}},
}

var benchHashers = []struct {
	name string
	new  func() cas.Hasher
}{
	{name: "sha256", new: func() cas.Hasher { return sha256.New() }},
	{name: "sha512_256", new: func() cas.Hasher { return sha512_256.New() }},
}

func BenchmarkStorePut(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(mem.New(), jsoncodec.New[testNote](), sha256.New())
			note := benchNote(sz.size)
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := s.Put(ctx, note); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-put", sz.size)
		})
	}
}

func BenchmarkStoreGet(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(mem.New(), jsoncodec.New[testNote](), sha256.New())
			h, err := s.Put(ctx, benchNote(sz.size))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-get", sz.size)
		})
	}
}

func BenchmarkFSBackendPut(b *testing.B) {
	for _, layout := range []struct {
		name string
		opts []backend.Option
	}{
		{"flat", []backend.Option{fs.WithFanOut(0), fs.WithFanLevels(0)}},
		{"fanout-2-1", nil},
	} {
		for _, sz := range backendBenchSizes {
			b.Run(layout.name+"/"+sz.name, func(b *testing.B) {
				ctx := context.Background()
				s, err := fs.New(b.TempDir(), layout.opts...)
				if err != nil {
					b.Fatal(err)
				}
				// Distinct content per iteration: each Put creates a new
				// object (a realistic write workload) rather than
				// overwriting one hash. The counter spans 8 bytes, so the
				// content does not repeat within any realistic b.N.
				data := []byte(strings.Repeat("x", sz.size))
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					binary.BigEndian.PutUint64(data, uint64(i))
					h := digestData(data)
					if err := s.Put(ctx, h, bytes.NewReader(data)); err != nil {
						b.Fatal(err)
					}
				}
				benchmarkSummary(b, "fs-put/"+layout.name, sz.size)
			})
		}
	}
}

func BenchmarkFSBackendGet(b *testing.B) {
	for _, layout := range []struct {
		name string
		opts []backend.Option
	}{
		{"flat", []backend.Option{fs.WithFanOut(0), fs.WithFanLevels(0)}},
		{"fanout-2-1", nil},
	} {
		for _, sz := range backendBenchSizes {
			b.Run(layout.name+"/"+sz.name, func(b *testing.B) {
				ctx := context.Background()
				s, err := fs.New(b.TempDir(), layout.opts...)
				if err != nil {
					b.Fatal(err)
				}
				h := digestData(make([]byte, sz.size))
				data := strings.Repeat("x", sz.size)
				if err := s.Put(ctx, h, strings.NewReader(data)); err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					rc, err := s.Get(ctx, h)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := io.Copy(io.Discard, rc); err != nil {
						b.Fatal(err)
					}
					rc.Close()
				}
				benchmarkSummary(b, "fs-get/"+layout.name, sz.size)
			})
		}
	}
}

// BenchmarkMemBackendPut/Get measure the BYTE layer on the memory backend —
// the case defaults.md §6 pins ("memory-backend small Put/Get … ≤5 allocs/op").
// The Store-level cases above include codec, envelope and hashing costs and are
// deliberately not held to that number.
func BenchmarkMemBackendPut(b *testing.B) {
	for _, sz := range backendBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := mem.New()
			data := []byte(strings.Repeat("x", sz.size))
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				binary.BigEndian.PutUint64(data, uint64(i))
				if err := s.Put(ctx, digestData(data), bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "mem-put", sz.size)
		})
	}
}

func BenchmarkMemBackendGet(b *testing.B) {
	for _, sz := range backendBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := mem.New()
			h := digestData(make([]byte, sz.size))
			if err := s.Put(ctx, h, strings.NewReader(strings.Repeat("x", sz.size))); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				rc, err := s.Get(ctx, h)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, rc); err != nil {
					b.Fatal(err)
				}
				rc.Close()
			}
			benchmarkSummary(b, "mem-get", sz.size)
		})
	}
}

func BenchmarkStoreCodecHashRoundTrip(b *testing.B) {
	for _, sz := range benchSizes {
		note := benchNote(sz.size)
		for _, codec := range benchCodecs {
			for _, hasher := range benchHashers {
				b.Run(fmt.Sprintf("%s/%s/%s", codec.name, hasher.name, sz.name), func(b *testing.B) {
					ctx := context.Background()
					s := cas.New(mem.New(), codec.new(), hasher.new())
					b.SetBytes(int64(sz.size))
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; b.Loop(); i++ {
						h, err := s.Put(ctx, note)
						if err != nil {
							b.Fatal(err)
						}
						if _, err := s.Get(ctx, h); err != nil {
							b.Fatal(err)
						}
					}
					benchmarkSummary(b, fmt.Sprintf("store-codec-hash/%s/%s", codec.name, hasher.name), sz.size)
				})
			}
		}
	}
}

func BenchmarkCodecMarshalUnmarshal(b *testing.B) {
	for _, sz := range benchSizes {
		note := benchNote(sz.size)
		for _, codec := range benchCodecs {
			b.Run(fmt.Sprintf("%s/%s", codec.name, sz.name), func(b *testing.B) {
				c := codec.new()
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					data, err := c.Marshal(note)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := c.Unmarshal(data); err != nil {
						b.Fatal(err)
					}
				}
				benchmarkSummary(b, fmt.Sprintf("codec/%s", codec.name), sz.size)
			})
		}
	}
}

func BenchmarkHasherDigest(b *testing.B) {
	for _, sz := range benchSizes {
		payload := bytes.Repeat([]byte("x"), sz.size)
		for _, hasher := range benchHashers {
			b.Run(fmt.Sprintf("%s/%s", hasher.name, sz.name), func(b *testing.B) {
				h := hasher.new()
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					if _, err := h.Digest(bytes.NewReader(payload)); err != nil {
						b.Fatal(err)
					}
				}
				benchmarkSummary(b, fmt.Sprintf("hasher/%s", hasher.name), sz.size)
			})
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[testNote](), sha256.New())
	note := benchNote(1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		h, err := s.Put(ctx, note)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "round-trip", len(note.Title)+len(note.Body))
}

func BenchmarkVerify(b *testing.B) {
	ctx := context.Background()
	s, err := fs.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	data := strings.Repeat("verify", 1024)
	h := digestData([]byte(data))
	if err := s.Put(ctx, h, strings.NewReader(data)); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if err := s.Verify(ctx, h, sha256.New()); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "verify", len(data))
}

func BenchmarkParseDigest(b *testing.B) {
	valid := "sha256:" + strings.Repeat("ab", 32)
	invalid := "sha256:not-hex"
	b.Run("valid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := sha256.Parse(valid); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkSummary(b, "parse-digest/valid", 0)
	})
	b.Run("invalid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			sha256.Parse(invalid)
		}
		benchmarkSummary(b, "parse-digest/invalid", 0)
	})
}

// BenchmarkParallelPutGet exercises the lock-free read path under
// concurrency (performance §2).
func BenchmarkParallelPutGet(b *testing.B) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[testNote](), sha256.New())
	const objects = 64
	var digests []cas.Digest
	for i := 0; i < objects; i++ {
		h, err := s.Put(ctx, testNote{Title: fmt.Sprintf("obj-%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		digests = append(digests, h)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			h := digests[i%objects]
			i++
			if _, err := s.Get(ctx, h); err != nil {
				b.Fatal(err)
			}
		}
	})
	benchmarkSummary(b, "parallel-put-get", 0)
}
