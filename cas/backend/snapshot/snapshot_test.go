package snapshot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	membackend "github.com/dmundt/go-cask/cas/backend/mem"
	packbackend "github.com/dmundt/go-cask/cas/backend/packfs"
	"github.com/dmundt/go-cask/cas/hash/sha256"
)

func TestExportDeterministicAndImportAcrossBackends(t *testing.T) {
	ctx := context.Background()
	source := membackend.New()
	objects := map[string]cas.Digest{
		"alpha": sha256.Of([]byte("alpha")),
		"beta":  sha256.Of([]byte("beta")),
	}
	for value, digest := range objects {
		if err := source.Put(ctx, digest, strings.NewReader(value)); err != nil {
			t.Fatal(err)
		}
	}

	var first, second bytes.Buffer
	if err := Export(ctx, source, &first); err != nil {
		t.Fatal(err)
	}
	if err := Export(ctx, source, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated exports must be byte-identical")
	}

	t.Run("filesystem", func(t *testing.T) {
		destination, err := fsbackend.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		importAndCompare(t, destination, first.Bytes(), objects)
	})
	t.Run("packfs", func(t *testing.T) {
		destination, err := packbackend.New(t.TempDir(), packbackend.WithEnabled())
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		importAndCompare(t, destination, first.Bytes(), objects)
	})
}

func importAndCompare(t *testing.T, destination cas.Backend, archive []byte, objects map[string]cas.Digest) {
	t.Helper()
	if err := Import(context.Background(), destination, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	for value, digest := range objects {
		rc, err := destination.Get(context.Background(), digest)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		if closeErr := rc.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != value {
			t.Fatalf("payload for %s = %q, want %q", digest, got, value)
		}
	}
}

func TestImportRejectsMalformedArchiveWithoutWritingInvalidRecord(t *testing.T) {
	ctx := context.Background()
	source := membackend.New()
	digest := sha256.Of([]byte("payload"))
	if err := source.Put(ctx, digest, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(ctx, source, &archive); err != nil {
		t.Fatal(err)
	}

	destination := membackend.New()
	data := append([]byte(nil), archive.Bytes()...)
	binary.BigEndian.PutUint64(data[10:18], 2)
	if err := Import(ctx, destination, bytes.NewReader(data)); err == nil {
		t.Fatal("truncated second record must fail")
	}
	if stats, err := destination.Stats(ctx); err != nil {
		t.Fatal(err)
	} else if stats.ObjectCount != 1 {
		t.Fatalf("object count after partial import = %d, want 1", stats.ObjectCount)
	}
}

func TestImportRejectsLargeDeclaredCountWithoutPreallocating(t *testing.T) {
	data := make([]byte, headerSize)
	copy(data[:8], magic[:])
	binary.BigEndian.PutUint16(data[8:10], version)
	binary.BigEndian.PutUint64(data[10:], uint64(math.MaxInt))

	if err := Import(context.Background(), membackend.New(), bytes.NewReader(data)); err == nil {
		t.Fatal("Import with missing records must fail")
	}
}

func TestImportRejectsDuplicateDigest(t *testing.T) {
	ctx := context.Background()
	source := membackend.New()
	digest := sha256.Of([]byte("payload"))
	if err := source.Put(ctx, digest, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(ctx, source, &archive); err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), archive.Bytes()...)
	binary.BigEndian.PutUint64(data[10:18], 2)
	data = append(data, archive.Bytes()[18:]...)

	if err := Import(ctx, membackend.New(), bytes.NewReader(data)); err == nil {
		t.Fatal("duplicate digest must fail")
	}
}

// TestImportDoesNotAllocateDeclaredPayloadSize pins the bounded payload read:
// the record header is untrusted input, so a payloadSize far larger than the
// archive must fail on the bytes that are actually missing (io.ErrUnexpectedEOF)
// instead of allocating the declared amount — a ~42-byte archive claiming 1<<40
// must not reserve a terabyte.
func TestImportDoesNotAllocateDeclaredPayloadSize(t *testing.T) {
	digest := sha256.Of([]byte("payload"))
	data := append(archiveWithHeader(1), make([]byte, recordHeaderSize)...)
	binary.BigEndian.PutUint64(data[headerSize:headerSize+8], uint64(len(digest)))
	binary.BigEndian.PutUint64(data[headerSize+8:headerSize+16], 1<<40)
	data = append(data, digest...)

	err := Import(context.Background(), membackend.New(), bytes.NewReader(data))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Import(oversized declared payload) error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestExportAndImportHonorCanceledContext(t *testing.T) {
	source := membackend.New()
	digest := sha256.Of([]byte("payload"))
	if err := source.Put(context.Background(), digest, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Export(ctx, source, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export error = %v, want context.Canceled", err)
	}
	if err := Import(ctx, membackend.New(), bytes.NewReader(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Import error = %v, want context.Canceled", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type failAtWriter struct {
	call int
	fail int
}

func (w *failAtWriter) Write(p []byte) (int, error) {
	w.call++
	if w.call == w.fail {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestExportReportsWriterFailure(t *testing.T) {
	source := membackend.New()
	digest := sha256.Of([]byte("payload"))
	if err := source.Put(context.Background(), digest, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	if err := Export(context.Background(), source, failingWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Export error = %v, want io.ErrClosedPipe", err)
	}
}

type fakeBackend struct {
	digests []cas.Digest
	listErr error
	getErr  error
	get     io.ReadCloser
	putErr  error
}

type cancelOnGetBackend struct {
	fakeBackend
	cancel context.CancelFunc
}

func (f *cancelOnGetBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	rc, err := f.fakeBackend.Get(ctx, d)
	f.cancel()
	return rc, err
}

func (f *fakeBackend) Put(context.Context, cas.Digest, io.Reader) error { return f.putErr }
func (f *fakeBackend) Get(context.Context, cas.Digest) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.get, nil
}
func (f *fakeBackend) Exists(context.Context, cas.Digest) (bool, error) { return false, nil }
func (f *fakeBackend) Delete(context.Context, cas.Digest) error         { return nil }
func (f *fakeBackend) List(context.Context) ([]cas.Digest, error)       { return f.digests, f.listErr }
func (f *fakeBackend) Stats(context.Context) (*cas.Stats, error)        { return &cas.Stats{}, nil }

type closeErrorReader struct {
	io.Reader
	closeErr error
}

func (r closeErrorReader) Close() error { return r.closeErr }

type partialWriter struct {
	cancel context.CancelFunc
	calls  int
}

func (w *partialWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		n := len(p) / 2
		if n == 0 {
			n = 1
		}
		w.cancel()
		return n, nil
	}
	return 0, io.ErrShortWrite
}

type cancelAfterReadReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

type cancelOnCloseReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelOnCloseReader) Close() error {
	r.cancel()
	return nil
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type cancelWriter struct {
	cancel context.CancelFunc
	calls  int
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 4 {
		w.cancel()
	}
	return len(p), nil
}

type archiveThenNilReader struct {
	reader io.Reader
	done   bool
}

func (r *archiveThenNilReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, nil
	}
	n, err := r.reader.Read(p)
	if err == io.EOF {
		r.done = true
		return 0, nil
	}
	return n, err
}

func (r *cancelAfterReadReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.cancel()
	return n, err
}

type archiveThenErrorReader struct {
	reader io.Reader
	done   bool
}

func (r *archiveThenErrorReader) Read(p []byte) (int, error) {
	if !r.done {
		n, err := r.reader.Read(p)
		if err == io.EOF {
			r.done = true
			return 0, io.ErrClosedPipe
		}
		return n, err
	}
	return 0, io.ErrClosedPipe
}

func TestExportInputAndBackendErrors(t *testing.T) {
	ctx := context.Background()
	digest := sha256.Of([]byte("payload"))
	tests := []struct {
		name string
		src  cas.Backend
		w    io.Writer
		want string
	}{
		{"nil source", nil, &bytes.Buffer{}, "source backend is nil"},
		{"nil writer", &fakeBackend{}, nil, "writer is nil"},
		{"list error", &fakeBackend{listErr: io.ErrUnexpectedEOF}, &bytes.Buffer{}, "unexpected EOF"},
		{"invalid digest", &fakeBackend{digests: []cas.Digest{nil}}, &bytes.Buffer{}, "invalid digest"},
		{"get error", &fakeBackend{digests: []cas.Digest{digest}, getErr: io.ErrUnexpectedEOF}, &bytes.Buffer{}, "unexpected EOF"},
		{"read error", &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(errReader{})}, &bytes.Buffer{}, "unexpected EOF"},
		{"close error", &fakeBackend{digests: []cas.Digest{digest}, get: closeErrorReader{Reader: strings.NewReader("x"), closeErr: io.ErrClosedPipe}}, &bytes.Buffer{}, "closed pipe"},
		{"record length write error", &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(strings.NewReader("x"))}, failingWriter{}, "closed pipe"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Export(ctx, test.src, test.w)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Export error = %v, want %q", err, test.want)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type trailingReadError struct{}

func (trailingReadError) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportHandlesPartialWritesAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	digest := sha256.Of([]byte("payload"))
	var source cas.Backend = &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(strings.NewReader("payload"))}
	writer := &partialWriter{cancel: cancel}
	if err := Export(ctx, source, writer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export error = %v, want context.Canceled", err)
	}

	second := sha256.Of([]byte("second"))
	ctx, cancel = context.WithCancel(context.Background())
	source = &fakeBackend{
		digests: []cas.Digest{digest, second},
		get: io.NopCloser(&cancelAfterReadReader{
			reader: strings.NewReader("payload"),
			cancel: cancel,
		}),
	}
	if err := Export(ctx, source, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export loop cancellation error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	source = &fakeBackend{
		digests: []cas.Digest{digest, second},
		get:     cancelOnCloseReader{Reader: strings.NewReader("payload"), cancel: cancel},
	}
	if err := Export(ctx, source, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export close cancellation error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	source = &cancelOnGetBackend{
		fakeBackend: fakeBackend{
			digests: []cas.Digest{digest, second},
			get:     io.NopCloser(strings.NewReader("payload")),
		},
		cancel: cancel,
	}
	if err := Export(ctx, source, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export Get cancellation error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	source = &fakeBackend{
		digests: []cas.Digest{digest, second},
		get:     io.NopCloser(strings.NewReader("payload")),
	}
	if err := Export(ctx, source, &cancelWriter{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Export writer cancellation error = %v, want context.Canceled", err)
	}
}

func TestExportReportsRecordWriteFailures(t *testing.T) {
	digest := sha256.Of([]byte("payload"))
	source := &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(strings.NewReader("payload"))}
	for _, call := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("write-%d", call), func(t *testing.T) {
			source := &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(strings.NewReader("payload"))}
			err := Export(context.Background(), source, &failAtWriter{fail: call})
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("Export error = %v, want io.ErrClosedPipe", err)
			}
		})
	}
	source = &fakeBackend{digests: []cas.Digest{digest}, get: io.NopCloser(strings.NewReader("payload"))}
	if err := Export(context.Background(), source, zeroWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Export short write error = %v, want io.ErrShortWrite", err)
	}
}

func archiveWithHeader(count uint64) []byte {
	data := make([]byte, headerSize)
	copy(data[:8], magic[:])
	binary.BigEndian.PutUint16(data[8:10], version)
	binary.BigEndian.PutUint64(data[10:], count)
	return data
}

func TestImportInputValidation(t *testing.T) {
	ctx := context.Background()
	validDigest := sha256.Of([]byte("payload"))
	validArchive := func() []byte {
		var buf bytes.Buffer
		source := membackend.New()
		if err := source.Put(ctx, validDigest, strings.NewReader("payload")); err != nil {
			t.Fatal(err)
		}
		if err := Export(ctx, source, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"nil destination", validArchive, "destination backend is nil"},
		{"nil reader", nil, "reader is nil"},
		{"short header", archiveWithHeader(0)[:headerSize-1], "EOF"},
		{"bad magic", append([]byte("bad"), archiveWithHeader(0)[3:]...), "invalid magic"},
		{"bad version", func() []byte {
			data := archiveWithHeader(0)
			binary.BigEndian.PutUint16(data[8:10], version+1)
			return data
		}(), "unsupported version"},
		{"count too large", archiveWithHeader(^uint64(0)), "object count is too large"},
		{"short lengths", append(archiveWithHeader(1), make([]byte, recordHeaderSize-1)...), "EOF"},
		{"zero digest size", append(archiveWithHeader(1), make([]byte, recordHeaderSize)...), "invalid digest"},
		{"oversized digest", func() []byte {
			data := append(archiveWithHeader(1), make([]byte, recordHeaderSize)...)
			binary.BigEndian.PutUint64(data[headerSize:headerSize+8], maxDigestSize+1)
			return data
		}(), "invalid digest size"},
		{"short digest", func() []byte {
			data := append(archiveWithHeader(1), make([]byte, recordHeaderSize)...)
			binary.BigEndian.PutUint64(data[headerSize:headerSize+8], uint64(len(validDigest)))
			return data
		}(), "EOF"},
		{"short payload", func() []byte {
			data := append(archiveWithHeader(1), make([]byte, recordHeaderSize)...)
			binary.BigEndian.PutUint64(data[headerSize:headerSize+8], uint64(len(validDigest)))
			binary.BigEndian.PutUint64(data[headerSize+8:headerSize+16], 1)
			return append(data, validDigest...)
		}(), "EOF"},
		{"oversized payload", func() []byte {
			data := append(archiveWithHeader(1), make([]byte, recordHeaderSize)...)
			binary.BigEndian.PutUint64(data[headerSize:headerSize+8], uint64(len(validDigest)))
			binary.BigEndian.PutUint64(data[headerSize+8:headerSize+16], ^uint64(0))
			return append(data, validDigest...)
		}(), "payload is too large"},
		{"trailing data", append(validArchive, 1), "trailing data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var destination cas.Backend = membackend.New()
			if test.name == "nil destination" {
				destination = nil
			}
			var reader io.Reader = bytes.NewReader(test.data)
			if test.name == "nil reader" {
				reader = nil
			}
			err := Import(ctx, destination, reader)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Import error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestImportBackendPutAndTrailingReaderErrors(t *testing.T) {
	ctx := context.Background()
	source := membackend.New()
	digest := sha256.Of([]byte("payload"))
	if err := source.Put(ctx, digest, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Export(ctx, source, &archive); err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, &fakeBackend{putErr: io.ErrClosedPipe}, bytes.NewReader(archive.Bytes())); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Import Put error = %v, want io.ErrClosedPipe", err)
	}

	if err := Import(ctx, membackend.New(), trailingReadError{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Import trailing read error = %v, want io.ErrClosedPipe", err)
	}
	if err := Import(ctx, membackend.New(), &archiveThenErrorReader{reader: bytes.NewReader(archive.Bytes())}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Import post-archive read error = %v, want io.ErrClosedPipe", err)
	}
	if err := Import(ctx, membackend.New(), &archiveThenNilReader{reader: bytes.NewReader(archive.Bytes())}); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("Import nil-progress reader error = %v, want trailing data", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	if err := Import(canceled, membackend.New(), &cancelAfterReadReader{
		reader: bytes.NewReader(archive.Bytes()),
		cancel: cancel,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Import loop cancellation error = %v, want context.Canceled", err)
	}
}
