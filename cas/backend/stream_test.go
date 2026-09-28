package backend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// readerFunc adapts a function to io.Reader so a test can pin the error a read
// reports without a real file.
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// TestReadAllFillsExactlyAndReportsShortStreams pins ReadAll's contract: it
// fills data completely, a stream that ends early is io.ErrUnexpectedEOF (not a
// silent partial fill), and a read error surfaces unwrapped.
func TestReadAllFillsExactlyAndReportsShortStreams(t *testing.T) {
	ctx := context.Background()

	buf := make([]byte, 4)
	if err := ReadAll(ctx, strings.NewReader("abcd"), buf); err != nil {
		t.Fatalf("ReadAll(full) = %v, want nil", err)
	}
	if string(buf) != "abcd" {
		t.Fatalf("ReadAll(full) filled %q, want %q", buf, "abcd")
	}

	buf = make([]byte, 4)
	if err := ReadAll(ctx, strings.NewReader("ab"), buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAll(short) = %v, want io.ErrUnexpectedEOF", err)
	}

	readErr := errors.New("read failed")
	buf = make([]byte, 4)
	if err := ReadAll(ctx, readerFunc(func([]byte) (int, error) { return 0, readErr }), buf); !errors.Is(err, readErr) {
		t.Fatalf("ReadAll(failing reader) = %v, want the reader's error", err)
	}

	// A zero-length read is a no-op that never touches the reader.
	if err := ReadAll(ctx, readerFunc(func([]byte) (int, error) {
		t.Error("ReadAll(len 0) read from the stream")
		return 0, nil
	}), nil); err != nil {
		t.Fatalf("ReadAll(len 0) = %v, want nil", err)
	}
}

// TestReadAllStopsOnCancelledContext pins that a cancelled context stops the
// read even when the stream itself would succeed.
func TestReadAllStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReadAll(ctx, strings.NewReader("abcd"), make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadAll(cancelled) = %v, want context.Canceled", err)
	}
}

// TestWriteAllReportsWriterErrors pins the two write-side failures WriteAll must
// distinguish: a writer error is returned as-is, and a writer that fails only
// after a successful partial write stops the loop with that error rather than
// retrying the remainder forever.
func TestWriteAllReportsWriterErrors(t *testing.T) {
	ctx := context.Background()

	writeErr := errors.New("write failed")
	if err := WriteAll(ctx, writerFunc(func([]byte) (int, error) { return 0, writeErr }), []byte("xyz")); !errors.Is(err, writeErr) {
		t.Fatalf("WriteAll(failing writer) = %v, want the writer's error", err)
	}

	var got []byte
	partialErr := errors.New("partial write failed")
	err := WriteAll(ctx, writerFunc(func(p []byte) (int, error) {
		got = append(got, p[0])
		return 1, partialErr
	}), []byte("xyz"))
	if !errors.Is(err, partialErr) {
		t.Fatalf("WriteAll(partial then error) = %v, want the writer's error", err)
	}
	if string(got) != "x" {
		t.Fatalf("WriteAll(partial then error) wrote %q, want %q before failing", got, "x")
	}

	if err := WriteAll(ctx, writerFunc(func([]byte) (int, error) { return 0, nil }), nil); err != nil {
		t.Fatalf("WriteAll(empty) = %v, want nil", err)
	}
}

// TestReadPayloadStopsOnReaderError pins that a payload read reports the
// underlying stream error rather than a length mismatch.
func TestReadPayloadStopsOnReaderError(t *testing.T) {
	readErr := errors.New("read failed")
	if _, err := ReadPayload(context.Background(), readerFunc(func([]byte) (int, error) { return 0, readErr }), 4); !errors.Is(err, readErr) {
		t.Fatalf("ReadPayload(failing reader) = %v, want the reader's error", err)
	}
}

// TestReadWholeSizesOneBuffer pins the point of ReadWhole (go-cask#385): with a
// length hinted — by the reader itself or by the caller — the returned buffer
// has exactly the capacity that length asked for, so the object was allocated
// once and never grown, which is what io.ReadAll's doubling costs.
func TestReadWholeSizesOneBuffer(t *testing.T) {
	ctx := context.Background()
	data := []byte(strings.Repeat("x", 128))

	got, err := ReadWhole(ctx, bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("ReadWhole(bytes.Reader) = %v", err)
	}
	if !bytes.Equal(got, data) || cap(got) != len(data) {
		t.Fatalf("ReadWhole(bytes.Reader) = %d bytes at capacity %d, want %d at capacity %d", len(got), cap(got), len(data), len(data))
	}

	// A reader that declares nothing but whose length the caller knows: the
	// declared value sizes the buffer too.
	got, err = ReadWhole(ctx, io.LimitReader(strings.NewReader(string(data)), int64(len(data))), int64(len(data)))
	if err != nil {
		t.Fatalf("ReadWhole(declared) = %v", err)
	}
	if !bytes.Equal(got, data) || cap(got) != len(data) {
		t.Fatalf("ReadWhole(declared) = %d bytes at capacity %d, want %d at capacity %d", len(got), cap(got), len(data), len(data))
	}
}

// TestReadWholeNeverTrustsTheLength pins the rule ReadWhole shares with
// ReadPayload: a length is a pre-allocation size, never a read limit. A stream
// that holds more than it declares is read whole, and one that holds less is
// returned as what it delivered.
func TestReadWholeNeverTrustsTheLength(t *testing.T) {
	ctx := context.Background()
	long := []byte(strings.Repeat("y", 4096))

	// The reader's own declaration is short; the stream must still be drained.
	got, err := ReadWhole(ctx, bytes.NewReader(long), 8)
	if err != nil {
		t.Fatalf("ReadWhole(short declaration) = %v", err)
	}
	if !bytes.Equal(got, long) {
		t.Fatalf("ReadWhole(short declaration) returned %d bytes, want the stream's %d", len(got), len(long))
	}
	if cap(got) < 8 {
		t.Fatalf("ReadWhole(short declaration) started below the hint: capacity %d", cap(got))
	}

	// A declaration longer than the stream is not an error and not a
	// truncation: the result is exactly what arrived.
	short := []byte("abc")
	got, err = ReadWhole(ctx, readerFunc(func(p []byte) (int, error) {
		if len(short) == 0 {
			return 0, io.EOF
		}
		n := copy(p, short)
		short = short[n:]
		return n, nil
	}), 1<<20)
	if err != nil {
		t.Fatalf("ReadWhole(long declaration) = %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("ReadWhole(long declaration) = %q, want %q", got, "abc")
	}
}

// TestReadWholeCeilingBoundsThePreallocation pins the guard on the hint: a
// length read from metadata (an object's physical size) may be stale or absurd,
// so the one-shot buffer stops at maxPrealloc and the read grows past it — the
// bytes are always all delivered.
func TestReadWholeCeilingBoundsThePreallocation(t *testing.T) {
	got, err := ReadWhole(context.Background(), bytes.NewReader([]byte("tiny")), maxPrealloc+1)
	if err != nil {
		t.Fatalf("ReadWhole(over the ceiling) = %v", err)
	}
	if string(got) != "tiny" {
		t.Fatalf("ReadWhole(over the ceiling) = %q, want %q", got, "tiny")
	}
	if cap(got) != maxPrealloc {
		t.Fatalf("ReadWhole(over the ceiling) allocated %d, want the ceiling %d", cap(got), maxPrealloc)
	}
}

// TestReadWholeWithoutAHintGrows pins the fallback: with nothing to size from,
// the read behaves exactly as io.ReadAll did — every byte, capacity grown by
// the stream itself. Nothing in this path reads a declared length.
func TestReadWholeWithoutAHintGrows(t *testing.T) {
	ctx := context.Background()
	data := []byte(strings.Repeat("z", 100))

	got, err := ReadWhole(ctx, readerFunc(func(p []byte) (int, error) {
		if len(data) == 0 {
			return 0, io.EOF
		}
		n := copy(p, data)
		data = data[n:]
		return n, nil
	}), 0)
	if err != nil {
		t.Fatalf("ReadWhole(no hint) = %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("ReadWhole(no hint) = %d bytes, want 100", len(got))
	}

	// A reader that declares zero bytes but delivers some is the same case: the
	// declaration is not a limit.
	got, err = ReadWhole(ctx, bytes.NewReader([]byte("abc")), 0)
	if err != nil {
		t.Fatalf("ReadWhole(zero-length reader) = %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("ReadWhole(zero-length reader) = %q, want %q", got, "abc")
	}

	// An empty stream is an empty result, not an error.
	empty, err := ReadWhole(ctx, bytes.NewReader(nil), 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ReadWhole(empty) = (%q, %v), want no bytes and no error", empty, err)
	}
}

// TestReadWholeReportsReadErrorsAndCancellation pins the two failures: a read
// error is returned with the bytes already delivered (io.ReadAll's shape), and
// a canceled context stops both before the first read and between reads.
func TestReadWholeReportsReadErrorsAndCancellation(t *testing.T) {
	readErr := errors.New("read failed")
	got, err := ReadWhole(context.Background(), readerFunc(func(p []byte) (int, error) {
		if len(p) > 0 {
			p[0] = 'a'
		}
		return 1, readErr
	}), 0)
	if !errors.Is(err, readErr) {
		t.Fatalf("ReadWhole(failing reader) = %v, want the reader's error", err)
	}
	if string(got) != "a" {
		t.Fatalf("ReadWhole(failing reader) = %q, want the byte delivered before the failure", got)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadWhole(canceled, bytes.NewReader([]byte("x")), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadWhole(canceled) = %v, want context.Canceled", err)
	}

	// A context canceled mid-stream stops the loop rather than reading to EOF.
	ctx, cancelMid := context.WithCancel(context.Background())
	calls := 0
	_, err = ReadWhole(ctx, readerFunc(func(p []byte) (int, error) {
		calls++
		if calls == 2 {
			cancelMid()
		}
		return copy(p, "abc"), nil
	}), 8)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadWhole(canceled mid-stream) = %v, want context.Canceled", err)
	}
}
