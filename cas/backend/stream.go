package backend

import (
	"context"
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
