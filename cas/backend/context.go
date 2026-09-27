package backend

import (
	"context"
	"io"
	"sync"
)

// copyBufSize is the scratch size io.Copy allocates for a generic copy — the
// size the pooled buffer below reuses instead of allocating it per call.
const copyBufSize = 32 * 1024

// copyBufPool holds the scratch buffers WriteTo copies through — process-wide
// pool scratch in the shape cas/codec/flate records (go-cask#378), never
// configuration. A buffer is borrowed for the duration of one WriteTo call and
// returned before it returns, so no call observes another call's bytes: it
// carries no state a caller can read, and the next call overwrites whatever the
// previous one left in it.
var copyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, copyBufSize)
		return &b
	},
}

// ContextReader wraps an io.Reader and aborts reads when the context is done.
// It centralizes the cancellation behavior shared by filesystem, in-memory, and
// packing backends so Put paths do not duplicate the same read loop logic.
//
// The zero value is valid and harmless: a nil Ctx means "no cancellation" and a
// nil R behaves like an exhausted reader (io.EOF), so a partially built
// ContextReader can never panic.
type ContextReader struct {
	// Ctx controls cancellation of reads. Nil means reads are never canceled.
	Ctx context.Context
	// R is the underlying reader. Nil reads as immediately exhausted (io.EOF).
	R io.Reader
}

// Read reads from R unless Ctx is canceled. A nil Ctx disables the cancellation
// check and a nil R returns io.EOF without panicking.
func (c ContextReader) Read(p []byte) (int, error) {
	if c.Ctx != nil {
		if err := c.Ctx.Err(); err != nil {
			return 0, err
		}
	}
	if c.R == nil {
		return 0, io.EOF
	}
	return c.R.Read(p)
}

// WriteTo copies R into w — checking Ctx before every read, and returning the
// number of bytes written — through a pooled scratch buffer.
//
// It exists because io.Copy checks WriterTo before ReaderFrom: without it,
// io.Copy(dst, ContextReader{...}) fell through to a destination's ReaderFrom
// (*os.File's, in every backend Put) whose generic fallback allocates a fresh
// 32 KiB buffer per call (go-cask#368). With it, the same io.Copy reuses one
// buffer across calls, and a small Put allocates no copy scratch at all.
//
// It deliberately does NOT hand a WriterTo on R to w: a source that offers one
// (an *os.File, a *bytes.Reader) would then stream to completion without a
// single Ctx check, weakening the "Put aborts on a canceled ctx" contract
// fs.Backend.Put promises (cas-core §4.4). The loop below keeps Read's
// per-read check. It mirrors io.Copy's own loop, including treating only an
// exact io.EOF as a clean end of stream.
func (c ContextReader) WriteTo(w io.Writer) (int64, error) {
	bufp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufp)
	buf := *bufp

	var written int64
	for {
		if c.Ctx != nil {
			if err := c.Ctx.Err(); err != nil {
				return written, err
			}
		}
		if c.R == nil {
			return written, nil
		}
		n, rerr := c.R.Read(buf)
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			written += int64(wn)
			if werr != nil {
				return written, werr
			}
			if wn < n {
				return written, io.ErrShortWrite
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return written, nil
			}
			return written, rerr
		}
	}
}
