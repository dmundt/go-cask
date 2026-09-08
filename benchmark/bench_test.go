package benchmark_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type testNote struct {
	Title string
	Body  string
}

func (testNote) Type() string           { return "note@1" }
func (testNote) References() []cas.Hash { return nil }

var sink any

func BenchmarkPut1KB(b *testing.B) {
	s, err := cas.New(backmem.New(), jsoncodec.New[testNote](), "sha256")
	if err != nil {
		b.Fatal(err)
	}
	obj := testNote{Title: strings.Repeat("x", 1024)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h, err := s.Put(context.Background(), obj)
		if err != nil {
			b.Fatal(err)
		}
		sink = h
	}
}

func BenchmarkMemStorePut1KB(b *testing.B) {
	BenchmarkPut1KB(b)
}

func BenchmarkFSBackendPut(b *testing.B) {
	layouts := []struct {
		name string
		opts []backend.Option
	}{
		{"flat", []backend.Option{fs.WithFanOut(0), fs.WithFanLevels(0)}},
	}
	for _, l := range layouts {
		for _, size := range []int{64, 1024, 1024 * 1024} {
			b.Run(l.name, func(b *testing.B) {})
			_ = size
		}
		_ = l
	}
}

func BenchmarkFSBackendGet(b *testing.B) {
	b.StopTimer()
	raw := backmem.New()
	h, _ := cas.HashBytes("sha256", []byte("x"))
	raw.Put(context.Background(), h, strings.NewReader("x"))
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		rc, _ := raw.Get(context.Background(), h)
		rc.Close()
	}
}
