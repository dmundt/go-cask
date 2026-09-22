package backend

import (
	"context"
	"io"
)

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
