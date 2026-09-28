package cas

import (
	"fmt"
	"io"
)

// closeOrder is which of the two failures a read-then-close path reports when
// both the read and the close fail. The rule is a decision, not an accident, so
// every path states it instead of imitating a neighbour (go-cask#340).
type closeOrder uint8

const (
	// readWins reports the read failure and reports a close failure only when
	// the read succeeded: a close failure must not mask the cause. The batch
	// path chose it deliberately for the same reason — there the "read" is the
	// caller's fn.
	readWins closeOrder = iota
	// closeWins reports the close failure first, whether or not the read
	// failed: a reader that cannot be released is the caller's problem to hear
	// about first. When the close succeeded, the read failure is still
	// reported, so nothing is lost. cas.Header chose it deliberately.
	closeWins
)

// readThenClose runs read over rc, closes rc exactly once, and returns the value
// together with the one failure order names. It is the one implementation of
// "read the object, then close the reader, and decide which failure to report"
// (go-cask#340): GetRaw, peekAt, Verify and the batch path all call it, and
// cas.Header calls it with the other order, so the precedence is visible at each
// site instead of being re-derived.
//
// readErr and closeErr render the caller's message for each failure — the error
// strings differ per operation and are operator output, so this helper composes
// no message of its own. A nil renderer returns the error unchanged, which is
// how peekAt keeps the parser's ErrCorrupt as its own answer.
//
// rc is closed even when read panics, so no path leaks a reader.
func readThenClose[T any](
	rc io.ReadCloser,
	read func(io.Reader) (T, error),
	order closeOrder,
	readErr, closeErr func(error) error,
) (v T, err error) {
	var rerr error
	defer func() {
		cerr := rc.Close()
		if order == closeWins {
			if cerr != nil {
				var zero T
				v, err = zero, render(closeErr, cerr)
			}
			return
		}
		// readWins: the read failure, if there is one, is already in err.
		if cerr != nil && err == nil {
			var zero T
			v, err = zero, render(closeErr, cerr)
		}
	}()
	v, rerr = read(rc)
	if rerr != nil {
		var zero T
		return zero, render(readErr, rerr)
	}
	return v, nil
}

// render applies a caller's message to an error, or returns the error unchanged
// when the caller composes no message for that failure.
func render(renderErr func(error) error, err error) error {
	if renderErr == nil {
		return err
	}
	return renderErr(err)
}

// wrap renders an error as "<prefix>: <err>", the shape every read-then-close
// caller's message has. It exists so a call site reads as the decision it is
// making — readThenClose(rc, io.ReadAll, readWins, wrap("cas: read object"),
// wrap("cas: close object")) — rather than as a closure per failure.
func wrap(prefix string) func(error) error {
	return func(err error) error { return fmt.Errorf("%s: %w", prefix, err) }
}
