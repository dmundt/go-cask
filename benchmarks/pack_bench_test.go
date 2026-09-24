package benchmark_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/pack"
)

func BenchmarkPackSplitJoin(b *testing.B) {
	for _, tc := range benchSizes {
		b.Run(tc.name, func(b *testing.B) {
			payload := bytes.Repeat([]byte("abc123xyz"), tc.size/9+1)
			payload = payload[:tc.size]
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for i := 0; i < b.N; i++ {
				parts := pack.Split(payload, 1024)
				out := pack.Join(parts)
				if len(out) != len(payload) {
					b.Fatalf("Join(Split(x)) size mismatch: got %d, want %d", len(out), len(payload))
				}
			}
		})
	}
}

func BenchmarkPackManifestRoundTrip(b *testing.B) {
	for _, tc := range benchSizes {
		b.Run(tc.name, func(b *testing.B) {
			meta := pack.Data{
				"kind":  "artifact",
				"name":  benchText(tc.size, 7),
				"owner": "team-a",
				"sha":   benchText(min(tc.size, 128), 11),
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(meta["name"]) + len(meta["sha"]) + len(meta["kind"]) + len(meta["owner"])))
			for i := 0; i < b.N; i++ {
				path := filepath.Join(b.TempDir(), "meta.json")
				if err := pack.SaveWith(b.Context(), path, meta, jsoncodec.New[pack.Data]()); err != nil {
					b.Fatalf("SaveWith() = %v", err)
				}
				loaded, err := pack.LoadWith(b.Context(), path, jsoncodec.New[pack.Data]())
				if err != nil {
					b.Fatalf("LoadWith() = %v", err)
				}
				if loaded["kind"] != meta["kind"] || loaded["owner"] != meta["owner"] {
					b.Fatalf("round trip mismatch: got %#v, want %#v", loaded, meta)
				}
			}
		})
	}
}

func BenchmarkPackManifestSaveLoadFile(b *testing.B) {
	payload := pack.Data{"kind": "artifact", "owner": "team-a", "note": benchText(4*1024, 9)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(b.TempDir(), "meta.json")
		if err := pack.SaveWith(b.Context(), path, payload, jsoncodec.New[pack.Data]()); err != nil {
			b.Fatalf("SaveWith() = %v", err)
		}
		if _, err := pack.LoadWith(b.Context(), path, jsoncodec.New[pack.Data]()); err != nil {
			b.Fatalf("LoadWith() = %v", err)
		}
		_ = os.Remove(path)
	}
}
