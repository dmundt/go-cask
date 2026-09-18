package backend

import (
	"context"
	"io"
)

// ContextReader wraps an io.Reader and aborts reads when the context is done.
// It centralizes the cancellation behavior shared by filesystem and in-memory
// backends so Put paths do not duplicate the same read loop logic.
type ContextReader struct {
	// Ctx controls cancellation of reads.
	Ctx context.Context
	// R is the underlying reader.
	R io.Reader
}

// Read reads from R unless Ctx is canceled.
func (c ContextReader) Read(p []byte) (int, error) {
	if err := c.Ctx.Err(); err != nil {
		return 0, err
	}
	return c.R.Read(p)
}
