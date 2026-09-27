package gzip_test

import (
	"bytes"
	stdgzip "compress/gzip"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/cas/codec/gzip"
	"github.com/dmundt/go-cask/cas/codec/json"
)

// poolSample is this file's payload type: gzip_test.go and fuzz_test.go already
// own theirs.
type poolSample struct {
	Title string
	Body  string
	Data  []byte
}

// TestCodecEncodeBytesMatchFreshWriter pins the compatibility the pool must not
// change: a writer taken from the pool and Reset writes exactly the bytes a
// newly built stdlib writer writes for the same payload. Three encodes in a row
// run the first on a constructed writer and the rest on a pooled one, so a
// Reset that left state behind would surface here as a mismatch instead of as a
// store that wrote an object another process cannot read.
func TestCodecEncodeBytesMatchFreshWriter(t *testing.T) {
	inner := json.New[poolSample]()
	want := poolSample{Title: "hello", Body: "world", Data: []byte("compressed payload")}
	payload, err := inner.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() []byte {
		var buf bytes.Buffer
		w := stdgzip.NewWriter(&buf)
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	codec := gzip.New(inner)
	for i := range 3 {
		got, err := codec.Encode(want)
		if err != nil {
			t.Fatalf("Encode %d = %v", i, err)
		}
		if !bytes.Equal(got, fresh()) {
			t.Fatalf("Encode %d = %x, want the fresh writer's %x", i, got, fresh())
		}
	}

	// And the pooled decoder reads what an unpooled writer wrote: fresh() is the
	// stream the pre-pool codec produced.
	got, err := codec.Decode(fresh())
	if err != nil {
		t.Fatalf("Decode of an unpooled stream = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode of an unpooled stream = %#v, want %#v", got, want)
	}
}

// TestCodecConcurrentRoundTrip exercises the pool the way a store does: one
// codec value shared between goroutines, each running its own round trip. Under
// -race it also proves no two calls ever hold one pooled compressor or reader
// at the same time.
func TestCodecConcurrentRoundTrip(t *testing.T) {
	codec := gzip.New(json.New[poolSample]())
	const (
		goroutines = 8
		rounds     = 40
	)

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range rounds {
				want := poolSample{
					Title: "concurrent",
					Body:  strings.Repeat("payload", 1+i%16),
					Data:  []byte{byte(g), byte(i)},
				}
				data, err := codec.Encode(want)
				if err != nil {
					t.Errorf("goroutine %d: Encode = %v", g, err)
					return
				}
				got, err := codec.Decode(data)
				if err != nil {
					t.Errorf("goroutine %d: Decode = %v", g, err)
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("goroutine %d: round trip = %#v, want %#v", g, got, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
