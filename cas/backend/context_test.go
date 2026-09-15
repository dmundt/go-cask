package backend

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestContextReaderReadsAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := ContextReader{Ctx: ctx, R: strings.NewReader("abc")}

	buf := make([]byte, 2)
	n, err := r.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read() error = %v, want nil or EOF", err)
	}
	if n != 2 || string(buf) != "ab" {
		t.Fatalf("Read() = (%d, %q), want (2, \"ab\")", n, string(buf))
	}

	cancel()
	_, err = r.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel = %v, want context.Canceled", err)
	}
	if err == nil {
		t.Fatal("Read() after cancel returned nil error")
	}
}

func TestContextReaderCancelledBeforeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := ContextReader{Ctx: ctx, R: strings.NewReader("hello")}
	buf := make([]byte, 1)
	_, err := r.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel = %v, want context.Canceled", err)
	}
}
