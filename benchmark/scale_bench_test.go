package benchmark_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type testNode struct {
	Name string
	Refs []cas.Hash
}

func (n testNode) Type() string           { return "node@1" }
func (n testNode) References() []cas.Hash { return n.Refs }

func BenchmarkScalePut(b *testing.B) {
	s, err := cas.New(backmem.New(), jsoncodec.New[testNode](), "sha256")
	if err != nil {
		b.Fatal(err)
	}
	obj := testNode{Name: strings.Repeat("x", 256)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Put(context.Background(), obj); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScaleGet(b *testing.B) {
	s, err := cas.New(backmem.New(), jsoncodec.New[testNode](), "sha256")
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	obj := testNode{Name: "target"}
	h, _ := s.Put(ctx, obj)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScaleStats(b *testing.B) {
	raw, _ := fs.New(b.TempDir())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw.Stats(context.Background())
	}
}
