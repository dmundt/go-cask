package cas

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// stubReader is a reader whose Close can fail, so readThenClose's two
// precedence rules are pinned without a backend.
type stubReader struct {
	io.Reader
	closeErr error
	closed   int
}

func (r *stubReader) Close() error {
	r.closed++
	return r.closeErr
}

func closing(closeErr error) *stubReader {
	return &stubReader{Reader: strings.NewReader("bytes"), closeErr: closeErr}
}

// TestReadThenClosePrecedence is the decision the issue is about (go-cask#340),
// written down as a test: readWins reports the read failure and hides a close
// failure behind it, closeWins reports the close failure first — and each still
// reports the other failure when it is the only one.
func TestReadThenClosePrecedence(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")

	read := func(err error) func(io.Reader) (string, error) {
		return func(io.Reader) (string, error) {
			if err != nil {
				return "", err
			}
			return "value", nil
		}
	}

	cases := []struct {
		name    string
		order   closeOrder
		rerr    error
		cerr    error
		want    string
		wantErr error
	}{
		{"readWins/read", readWins, readErr, nil, "", readErr},
		{"readWins/close", readWins, nil, closeErr, "", closeErr},
		// Both failed: the read failure is the one reported, so a close
		// failure never masks the cause.
		{"readWins/both", readWins, readErr, closeErr, "", readErr},
		{"closeWins/read", closeWins, readErr, nil, "", readErr},
		{"closeWins/close", closeWins, nil, closeErr, "", closeErr},
		// Both failed: the close failure is the one reported — a reader that
		// cannot be released is the caller's problem to hear about first.
		{"closeWins/both", closeWins, readErr, closeErr, "", closeErr},
		{"readWins/ok", readWins, nil, nil, "value", nil},
		{"closeWins/ok", closeWins, nil, nil, "value", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := closing(tc.cerr)
			got, err := readThenClose(rc, read(tc.rerr), tc.order,
				wrap("cas: read object"), wrap("cas: close object"))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("readThenClose = %v, want %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), ": "+tc.wantErr.Error()) {
				t.Errorf("readThenClose error = %q, want the caller's message around %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("readThenClose value = %q, want %q", got, tc.want)
			}
			if rc.closed != 1 {
				t.Errorf("Close calls = %d, want exactly 1", rc.closed)
			}
		})
	}
}

// TestReadThenCloseNilRendererReturnsCause covers the renderer a caller omits
// (peekAt): the parser's own error, ErrCorrupt included, is the answer, so
// Store.Type reports exactly what the header parser found.
func TestReadThenCloseNilRendererReturnsCause(t *testing.T) {
	want := errors.New("peek failed")
	rc := closing(nil)
	_, err := readThenClose(rc, func(io.Reader) (string, error) { return "", want }, readWins, nil, wrap("cas: close object"))
	if err != want {
		t.Fatalf("readThenClose = %v, want the cause unchanged", err)
	}
	if rc.closed != 1 {
		t.Errorf("Close calls = %d, want exactly 1", rc.closed)
	}
}

// TestReadThenCloseClosesOnPanic pins the one behaviour the defer exists for:
// a read that panics still releases the reader.
func TestReadThenCloseClosesOnPanic(t *testing.T) {
	rc := closing(nil)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panicking read did not panic")
			}
		}()
		_, _ = readThenClose(rc, func(io.Reader) (string, error) { panic("read exploded") }, readWins, nil, nil)
	}()
	if rc.closed != 1 {
		t.Errorf("Close calls after a panicking read = %d, want exactly 1", rc.closed)
	}
}
