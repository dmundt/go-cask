package benchmark_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/gitlike"
)

func BenchmarkStorePut(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-put/steady-state/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			benchmarkWarmup(func() {
				if _, err := s.Put(ctx, benchNoteWithSeed(sz.size, 0)); err != nil {
					b.Fatal(err)
				}
			})
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := s.Put(ctx, benchNoteWithSeed(sz.size, i)); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-put/steady-state", sz.size)
		})
		b.Run(fmt.Sprintf("store-put/setup/cold-start/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			benchmarkWarmup(func() {
				s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
				if _, err := s.Put(ctx, benchNoteWithSeed(sz.size, 0)); err != nil {
					b.Fatal(err)
				}
			})
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
				if _, err := s.Put(ctx, benchNoteWithSeed(sz.size, i)); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-put/setup/cold-start", sz.size)
		})
	}
}

func BenchmarkStoreGetHot(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-get/steady-state/hot/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
			h, err := s.Put(ctx, benchNoteWithSeed(sz.size, 0))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-get/steady-state/hot", sz.size)
		})
	}
}

func BenchmarkStoreGetCold(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-get/setup/cold-start/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
				h, err := s.Put(ctx, benchNoteWithSeed(sz.size, i))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-get/setup/cold-start", sz.size)
		})
	}
}

func BenchmarkStoreGetMixed(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-get/steady-state/mixed-hot-cold/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
			hot := make([]cas.Digest, benchmarkHotSetSize)
			for i := range hot {
				h, err := s.Put(ctx, benchNoteWithSeed(sz.size, i))
				if err != nil {
					b.Fatal(err)
				}
				hot[i] = h
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if i%benchmarkMixedColdRatio == 0 {
					h, err := s.Put(ctx, benchNoteWithSeed(sz.size, i+benchmarkHotSetSize))
					if err != nil {
						b.Fatal(err)
					}
					if _, err := s.Get(ctx, h); err != nil {
						b.Fatal(err)
					}
					continue
				}
				if _, err := s.Get(ctx, hot[i%len(hot)]); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-get/steady-state/mixed-hot-cold", sz.size)
		})
	}
}

func BenchmarkStoreBaselineJSONSHA256(b *testing.B) {
	ctx := context.Background()
	backend, err := fs.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	s := cas.New(backend, jsoncodec.New[testNote](), sha256.New())
	note := benchNoteWithSeed(1024, 0)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		h, err := s.Put(ctx, benchNoteWithSeed(1024, i))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
		if err := backend.Verify(ctx, h, sha256.New()); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "store-baseline/json-sha256", len(note.Title)+len(note.Body))
}

func BenchmarkStoreWorkflowWriteReadVerify(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-workflow/steady-state/write-read-verify/%s", sz.name), func(b *testing.B) {
			ctx := context.Background()
			backend, err := fs.New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			s := cas.New(backend, jsoncodec.New[testNote](), sha256.New())
			hot := make([]cas.Digest, benchmarkHotSetSize)
			for i := range hot {
				h, err := s.Put(ctx, benchNoteWithSeed(sz.size, i))
				if err != nil {
					b.Fatal(err)
				}
				hot[i] = h
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			benchmarkWarmup(func() {
				note := benchNoteWithSeed(sz.size, 0)
				h, err := s.Put(ctx, note)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
				if err := backend.Verify(ctx, h, sha256.New()); err != nil {
					b.Fatal(err)
				}
			})
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				note := benchNoteWithSeed(sz.size, i+benchmarkHotSetSize)
				h, err := s.Put(ctx, note)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
				if err := backend.Verify(ctx, h, sha256.New()); err != nil {
					b.Fatal(err)
				}
				if i%benchmarkMixedColdRatio == 0 {
					if _, err := s.Get(ctx, hot[i%len(hot)]); err != nil {
						b.Fatal(err)
					}
				}
			}
			benchmarkSummary(b, "store-workflow/steady-state/write-read-verify", sz.size)
		})
	}
}

func BenchmarkStoreGraphTraversal(b *testing.B) {
	ctx := context.Background()
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("store-graph/steady-state/%s", sz.name), func(b *testing.B) {
			backend := backmem.New()
			repo := gitlike.NewRepository(backend, sha256.New(), gitlike.Codecs{
				Blob:   jsoncodec.New[*gitlike.Blob](),
				Tree:   jsoncodec.New[*gitlike.Tree](),
				Commit: jsoncodec.New[*gitlike.Commit](),
				Tag:    jsoncodec.New[*gitlike.Tag](),
			})
			resolver := gitlike.NewResolver(repo)
			blobHash, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte(benchText(sz.size, 0))})
			if err != nil {
				b.Fatal(err)
			}
			treeHash, err := repo.Trees.Put(ctx, &gitlike.Tree{Entries: []gitlike.TreeEntry{{Name: "hello.txt", Hash: blobHash, Mode: "100644"}}})
			if err != nil {
				b.Fatal(err)
			}
			commitHash, err := repo.Commits.Put(ctx, &gitlike.Commit{Tree: treeHash, Author: "bench", Message: "graph walk"})
			if err != nil {
				b.Fatal(err)
			}
			if _, err := resolver.ResolveCommit(ctx, commitHash); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if err := gitlike.WalkGraph(ctx, resolver, commitHash, func(o *gitlike.ResolvedObject) error {
					_ = o
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "store-graph/steady-state", sz.size)
		})
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	ctx := context.Background()
	s := cas.New(backmem.New(), jsoncodec.New[testNote](), sha256.New())
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		note := benchNoteWithSeed(1024, i)
		h, err := s.Put(ctx, note)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "store-round-trip/steady-state/1KiB", 1024)
}
