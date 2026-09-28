package backend

import (
	"context"
	"errors"
	"io"
)

// WriteAll writes data to w completely, honoring ctx between partial writes.
// A writer that reports no progress is reported as io.ErrShortWrite rather than
// spinning forever; a context cancellation or write error stops the loop and is
// returned unwrapped so callers can wrap it with their own context.
func WriteAll(ctx context.Context, w io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// sizeHint is implemented by readers that know how many bytes they can still
// deliver: *bytes.Reader, *bytes.Buffer, *strings.Reader. It is a capability of
// the reader, not a length declared inside the stream, so nothing untrusted is
// trusted here.
type sizeHint interface{ Len() int }

// maxPrealloc bounds the one-shot buffer ReadWhole will allocate from a length
// hint. A hint is metadata — a reader's own Len, an object's physical size —
// and a stale or lying one must not turn one Put or one export record into an
// unbounded allocation, so a longer object simply grows past the ceiling the
// way an unhinted read does.
const maxPrealloc = 64 << 20

// ReadWhole reads r to EOF into one buffer, honoring ctx cancellation. It is
// Reader's io.ReadAll with the doubling removed where the length is knowable
// (go-cask#385).
//
// declared is the length the caller already knows r holds, or <= 0 when it
// knows none: ReadWhole then uses r's own declaration when r has one (sizeHint
// — what Store.Put hands a backend, a *bytes.Reader). Either length is only a
// pre-allocation size, never a read limit: the read always runs to EOF, so a
// stream holding more or fewer bytes than declared is neither truncated nor
// trusted, and the returned slice is exactly what the stream delivered. The
// pre-allocation is capped at maxPrealloc.
//
// Without a hint the read grows as io.ReadAll does — growth from what the
// stream actually holds, never from a declared size. ReadPayload states the
// same rule for a length that arrived in an untrusted header, and keeps its own
// deliberate decision not to size anything from it.
func ReadWhole(ctx context.Context, r io.Reader, declared int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hint := declared
	if hint <= 0 {
		if sh, ok := r.(sizeHint); ok {
			hint = int64(sh.Len())
		}
	}
	if hint > maxPrealloc {
		hint = maxPrealloc
	}
	rd := ContextReader{Ctx: ctx, R: r}
	if hint <= 0 {
		// Nothing to size from: grow exactly as io.ReadAll does, from what the
		// stream delivers.
		return io.ReadAll(rd)
	}
	buf := make([]byte, 0, hint)
	for {
		if len(buf) < cap(buf) {
			n, err := rd.Read(buf[len(buf):cap(buf)])
			buf = buf[:len(buf)+n]
			if err != nil {
				if errors.Is(err, io.EOF) {
					return buf, nil
				}
				return buf, err
			}
			continue
		}
		// The buffer is full: probe a single byte so a stream that ends exactly
		// at its declared length costs no growth, while one that runs past the
		// declaration falls back to append's growth.
		var one [1]byte
		n, err := rd.Read(one[:])
		buf = append(buf, one[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return buf, err
		}
	}
}

// ReadAll fills data completely from r, honoring ctx between reads. A short
// read is reported as io.ErrUnexpectedEOF by io.ReadFull, so a truncated stream
// is distinguishable from a clean end of stream.
func ReadAll(ctx context.Context, r io.Reader, data []byte) error {
	_, err := io.ReadFull(ContextReader{Ctx: ctx, R: r}, data)
	return err
}

// ReadPayload reads a payload whose length was declared as size by an untrusted
// header, without trusting size for the allocation. It grows the buffer to what
// the stream actually holds — at most size bytes — and verifies the length
// exactly, so a header claiming a huge payload fails on the bytes that are
// actually missing instead of allocating the claimed amount, and a truncated
// stream is io.ErrUnexpectedEOF. A zero size reads nothing and returns an empty
// payload, so a record never over-reads into the next one. Callers must reject
// sizes that cannot be addressed (size > math.MaxInt) before calling.
func ReadPayload(ctx context.Context, r io.Reader, size uint64) ([]byte, error) {
	limited := io.LimitReader(ContextReader{Ctx: ctx, R: r}, int64(size))
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != size {
		return nil, io.ErrUnexpectedEOF
	}
	return data, nil
}
