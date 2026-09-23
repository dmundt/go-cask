package backend

import (
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
