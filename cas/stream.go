package cas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// PutStream spools r to a temporary file while hashing it, then stores it in
// backend under the digest of its content. It is the byte-layer counterpart of
// Store.Put: Store.Put computes the digest of an object it encodes itself, while
// this one serves a caller that holds a raw stream whose address is not known
// yet. It returns the digest and whether the object was already present.
//
// Deduplicated is true when backend.Exists reported the digest before the
// write, in which case the spool is discarded and no Put is attempted. An empty
// stream is stored like any other content — it is the object the empty digest
// addresses — so a caller that refuses an empty upload applies that rule
// itself, to the bytes it read.
//
// The algorithm is the caller's: hasher is injected exactly as it is for Store,
// and the core names none (cas-core §4.2). The spool is what makes a
// non-seekable reader storable — the bytes are written once, the digest is
// computed from the spooled copy, and the spool is then streamed into the
// backend — so the object is never buffered in memory and r is read exactly
// once. The spool is closed and removed on every path, including a failure.
//
// ctx is honored before the spool, before the digest read, and through the
// backend, so a cancelled call stops instead of storing anything.
func PutStream(ctx context.Context, backend Backend, hasher Hasher, r io.Reader) (d Digest, deduplicated bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if backend == nil {
		return nil, false, errors.New("cas: put stream: nil backend")
	}
	if hasher == nil {
		return nil, false, errors.New("cas: put stream: nil hasher")
	}
	spool, err := os.CreateTemp("", "cask-stream-*")
	if err != nil {
		return nil, false, fmt.Errorf("cas: put stream: create spool: %w", err)
	}
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
	}()

	if _, err := io.Copy(spool, r); err != nil {
		return nil, false, fmt.Errorf("cas: put stream: spool: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, err := spool.Seek(0, 0); err != nil {
		return nil, false, fmt.Errorf("cas: put stream: rewind spool: %w", err)
	}
	if d, err = hasher.Digest(spool); err != nil {
		return nil, false, fmt.Errorf("cas: put stream: digest: %w", err)
	}
	if deduplicated, err = backend.Exists(ctx, d); err != nil {
		return nil, false, err
	}
	if deduplicated {
		return d, true, nil
	}
	if _, err := spool.Seek(0, 0); err != nil {
		return nil, false, fmt.Errorf("cas: put stream: rewind spool: %w", err)
	}
	if err := backend.Put(ctx, d, spool); err != nil {
		return nil, false, err
	}
	return d, false, nil
}
