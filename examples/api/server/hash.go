package main

import (
	"context"
	"errors"
	"io"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// errEmptyUpload refuses an empty request body. The HTTP surface treats an empty
// upload as a bad request, not as the content-addressed empty object
// cas.PutStream stores like any other content, so the refusal lives here, beside
// the handler that answers 400 for it.
var errEmptyUpload = errors.New("cas api: empty upload")

// bodyCounter counts the bytes read from the request body, so the handler can
// apply its own minimum-size rule without asking the byte layer for a size it
// does not report (go-cask#342): cas.PutStream returns the digest and the
// deduplication, and the upload's contract — refuse an empty body — is decided
// where it is answered. Read passes through to the body it wraps.
type bodyCounter struct {
	body  io.Reader
	bytes int64
}

// Read implements io.Reader, counting what the wrapped body returns.
func (c *bodyCounter) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	c.bytes += int64(n)
	return n, err
}

// spoolAndPut streams body into the store through cas.PutStream — the one owner
// of the spool-while-hashing sequence, shared with the CLI's `put` (go-cask#342)
// — and reports the stored digest and whether the object was already present.
//
// It returns errEmptyUpload when the body was empty. cas.PutStream has already
// addressed and stored it by then: an empty object is a legitimate store entry
// (the CLI's `put` writes one), so the upload's minimum size is this surface's
// decision rather than the library's. The handler rejects a declared
// zero-length body before calling this, so the empty case here is a body whose
// length was not declared.
func spoolAndPut(ctx context.Context, backend cas.Backend, body io.Reader) (cas.Digest, bool, error) {
	counted := &bodyCounter{body: body}
	h, exists, err := cas.PutStream(ctx, backend, sha256.New(), counted)
	if err != nil {
		return nil, false, err
	}
	if counted.bytes == 0 {
		return nil, false, errEmptyUpload
	}
	return h, exists, nil
}
