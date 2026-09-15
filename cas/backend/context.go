package backend

import (
	"context"
	"io"
)

// ContextReader wraps an io.Reader and aborts reads when the context is done.
// It centralizes the cancellation behavior shared by filesystem and in-memory
// backends so Put paths do not duplicate the same read loop logic.
type ContextReader struct {
	Ctx context.Context
	R   io.Reader
}

func (c ContextReader) Read(p []byte) (int, error) {
	if err := c.Ctx.Err(); err != nil {
		return 0, err
	}
	return c.R.Read(p)
}
