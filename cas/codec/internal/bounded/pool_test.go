package bounded

import (
	"bytes"
	"errors"
	"io"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// poolAttempts bounds the retry loops below. sync.Pool may discard a Put — the
// race detector, which the gate runs, simulates that by dropping one Put in
// four — so a test of reuse retries instead of assuming the relation the
// sync.Pool documentation says callers cannot assume. Sixty-four attempts fail
// with probability 4^-64, and without the race detector the first attempt hits.
const poolAttempts = 64

// stubCompressor records pool traffic on a writer without compressing, so a
// test can assert which destination each call wrote to and whether a failed
// writer was parked.
type stubCompressor struct {
	resets   []io.Writer
	writes   int
	closes   int
	writeErr error
	closeErr error
}

func (c *stubCompressor) Reset(w io.Writer) { c.resets = append(c.resets, w) }

func (c *stubCompressor) Write(p []byte) (int, error) {
	c.writes++
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

func (c *stubCompressor) Close() error {
	c.closes++
	return c.closeErr
}

// stubDecompressor is the read-path twin: it records every source it was
// re-armed on and serves data on demand.
type stubDecompressor struct {
	resets   []io.Reader
	data     []byte
	readErr  error
	resetErr error
}

func (d *stubDecompressor) Reset(r io.Reader) error {
	d.resets = append(d.resets, r)
	return d.resetErr
}

func (d *stubDecompressor) Read(p []byte) (int, error) {
	if d.readErr != nil {
		return 0, d.readErr
	}
	if len(d.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, d.data)
	d.data = d.data[n:]
	return n, nil
}

func (d *stubDecompressor) Close() error { return nil }

// TestWriterPoolReusesWriter pins the pool's whole point: a writer parked by one
// call is handed to the next, re-armed on that call's destination instead of
// being built again, and parked on io.Discard in between so it never pins a
// caller's buffer. A miss builds directly onto the caller's destination, so the
// only Reset calls the reused writer sees are the park and the re-arm.
func TestWriterPoolReusesWriter(t *testing.T) {
	var destinations []io.Writer
	pool := NewWriterPool(func(w io.Writer) (Compressor, error) {
		destinations = append(destinations, w)
		return &stubCompressor{}, nil
	})

	var a bytes.Buffer
	parked, err := pool.get(&a)
	if err != nil {
		t.Fatalf("first get = %v", err)
	}
	if len(destinations) != 1 || destinations[0] != &a {
		t.Fatalf("first get built onto %v, want the caller's buffer", destinations)
	}

	var b bytes.Buffer
	reused := false
	for attempt := 0; attempt < poolAttempts && !reused; attempt++ {
		pool.put(parked)
		next, err := pool.get(&b)
		if err != nil {
			t.Fatalf("get = %v", err)
		}
		if reused = next == parked; !reused {
			parked = next
		}
	}
	if !reused {
		t.Fatal("the pool never handed a parked writer back")
	}

	stub, ok := parked.(*stubCompressor)
	if !ok {
		t.Fatalf("reused writer is a %T, want *stubCompressor", parked)
	}
	want := []io.Writer{io.Discard, &b}
	if len(stub.resets) != len(want) {
		t.Fatalf("resets = %v, want %v", stub.resets, want)
	}
	for i := range want {
		if stub.resets[i] != want[i] {
			t.Fatalf("reset %d = %v, want %v", i, stub.resets[i], want[i])
		}
	}
}

// TestWriterPoolDropsFailedWriter pins the error arm: a writer whose stream
// failed is not parked, so the next Encode builds a fresh one rather than
// inheriting a broken compressor.
func TestWriterPoolDropsFailedWriter(t *testing.T) {
	for _, tc := range []struct {
		name string
		new  func() Compressor
	}{
		{"write", func() Compressor { return &stubCompressor{writeErr: errors.New("write boom")} }},
		{"close", func() Compressor { return &stubCompressor{closeErr: errors.New("close boom")} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built := 0
			pool := NewWriterPool(func(io.Writer) (Compressor, error) {
				built++
				return tc.new(), nil
			})

			for range 2 {
				if _, err := Encode(jsoncodec.New[payload](), payload{Name: "demo"}, pool); err == nil {
					t.Fatal("compression failure should propagate")
				}
			}
			if built != 2 {
				t.Fatalf("constructor ran %d times, want 2: the failed writer was parked", built)
			}
		})
	}
}

// TestReaderPoolReusesReader is the read-path twin of
// TestWriterPoolReusesWriter: the parked reader is handed out again, re-armed
// on the second call's source, and parked on an empty source in between so it
// pins no payload.
func TestReaderPoolReusesReader(t *testing.T) {
	var sources []io.Reader
	pool := NewReaderPool(func(r io.Reader) (Decompressor, error) {
		sources = append(sources, r)
		return &stubDecompressor{}, nil
	})

	src := bytes.NewReader([]byte("first"))
	parked, err := pool.get(src)
	if err != nil {
		t.Fatalf("first get = %v", err)
	}
	if len(sources) != 1 || sources[0] != io.Reader(src) {
		t.Fatalf("first get read %v, want the caller's source", sources)
	}

	src2 := bytes.NewReader([]byte("second"))
	reused := false
	for attempt := 0; attempt < poolAttempts && !reused; attempt++ {
		pool.put(parked)
		next, err := pool.get(src2)
		if err != nil {
			t.Fatalf("get = %v", err)
		}
		if reused = next == parked; !reused {
			parked = next
		}
	}
	if !reused {
		t.Fatal("the pool never handed a parked reader back")
	}

	stub, ok := parked.(*stubDecompressor)
	if !ok {
		t.Fatalf("reused reader is a %T, want *stubDecompressor", parked)
	}
	want := []io.Reader{eofReader{}, src2}
	if len(stub.resets) != len(want) {
		t.Fatalf("resets = %v, want %v", stub.resets, want)
	}
	for i := range want {
		if stub.resets[i] != want[i] {
			t.Fatalf("reset %d = %v, want %v", i, stub.resets[i], want[i])
		}
	}
}

// TestReaderPoolReportsResetFailure pins that a pooled reader which cannot be
// re-armed reports the source's own error and is dropped: the failure belongs
// to the data, not to the pool.
func TestReaderPoolReportsResetFailure(t *testing.T) {
	boom := errors.New("header boom")
	pool := NewReaderPool(func(io.Reader) (Decompressor, error) {
		return &stubDecompressor{}, nil
	})

	for attempt := 0; attempt < poolAttempts; attempt++ {
		pool.pool.Put(&stubDecompressor{resetErr: boom})
		if _, err := pool.get(bytes.NewReader(nil)); errors.Is(err, boom) {
			return
		}
	}
	t.Fatal("the pool never handed a reader that cannot be re-armed back")
}

// TestReaderPoolDropsFailedReader pins that a reader which failed mid-stream is
// never parked: the next Decode builds a fresh one instead of reading through a
// damaged decompressor.
func TestReaderPoolDropsFailedReader(t *testing.T) {
	var built []*stubDecompressor
	pool := NewReaderPool(func(io.Reader) (Decompressor, error) {
		d := &stubDecompressor{readErr: errors.New("read boom")}
		built = append(built, d)
		return d, nil
	})

	for range 2 {
		if _, err := Decode(jsoncodec.New[payload](), []byte("bad"), MaxDecodedBytes, pool); err == nil {
			t.Fatal("read failure should propagate")
		}
	}
	if len(built) != 2 {
		t.Fatalf("constructor ran %d times, want 2: the failed reader was parked", len(built))
	}
	for i, d := range built {
		if len(d.resets) != 0 {
			t.Fatalf("reader %d was parked after a failed read: resets = %v", i, d.resets)
		}
	}
}

// TestReaderPoolDropsReaderOverTheCeiling is the same guard on the ceiling arm:
// a decoder stopped one byte past the limit is only part-way through its
// stream, so it is dropped rather than parked.
func TestReaderPoolDropsReaderOverTheCeiling(t *testing.T) {
	var built []*stubDecompressor
	pool := NewReaderPool(func(io.Reader) (Decompressor, error) {
		d := &stubDecompressor{data: []byte("0123456789")}
		built = append(built, d)
		return d, nil
	})

	for range 2 {
		if _, err := Decode(jsoncodec.New[payload](), []byte("bad"), 4, pool); !errors.Is(err, ErrDecodedTooLarge) {
			t.Fatalf("Decode over the ceiling = %v, want ErrDecodedTooLarge", err)
		}
	}
	if len(built) != 2 {
		t.Fatalf("constructor ran %d times, want 2: the stopped reader was parked", len(built))
	}
	for i, d := range built {
		if len(d.resets) != 0 {
			t.Fatalf("reader %d was parked after the ceiling stopped it: resets = %v", i, d.resets)
		}
	}
}

// TestEOFReaderReportsEndOfStream pins the parked source: it reports end of
// stream on both reads, which is what lets a pooled reader drop the payload it
// last decoded instead of pinning it.
func TestEOFReaderReportsEndOfStream(t *testing.T) {
	if _, err := (eofReader{}).Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("eofReader.Read = %v, want io.EOF", err)
	}
	if _, err := (eofReader{}).ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("eofReader.ReadByte = %v, want io.EOF", err)
	}
}
