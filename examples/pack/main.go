// Package main demonstrates the public chunking helper in the pack package.
//
// The example focuses on splitting, reassembling, and saving/loading chunked
// payloads using the canonical helper API rather than a backend-specific format.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/pack"
)

const usage = `usage: pack <command> [args]

commands:
  split <size> <payload>
  save <path> <kind> <owner> [payload]
  load <path>
  roundtrip <size> <payload>
`

type Chunk struct {
	Index int    `json:"index"`
	Size  int    `json:"size"`
	Data  string `json:"data"`
}

type Manifest struct {
	Kind      string  `json:"kind"`
	Owner     string  `json:"owner"`
	Chunks    []Chunk `json:"chunks,omitempty"`
	TotalSize int     `json:"total_size,omitempty"`
}

func splitPayload(payload []byte, chunkSize int) []Chunk {
	parts := pack.Split(payload, chunkSize)
	out := make([]Chunk, 0, len(parts))
	for i, part := range parts {
		out = append(out, Chunk{
			Index: i,
			Size:  len(part),
			Data:  string(part),
		})
	}
	return out
}

func (m Manifest) Reassemble() []byte {
	if len(m.Chunks) == 0 {
		return nil
	}
	out := make([]byte, 0, m.TotalSize)
	for _, chunk := range m.Chunks {
		out = append(out, []byte(chunk.Data)...)
	}
	return out
}

func asChunkBytes(chunks []Chunk) [][]byte {
	out := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, []byte(chunk.Data))
	}
	return out
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}

	switch args[0] {
	case "split":
		if len(args) != 3 {
			return errors.New(usage)
		}
		size, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid chunk size: %w", err)
		}
		return runSplit(args[2], size)
	case "save":
		if len(args) < 4 || len(args) > 5 {
			return errors.New(usage)
		}
		payload := ""
		if len(args) == 5 {
			payload = args[4]
		}
		return runSave(args[1], args[2], args[3], payload)
	case "load":
		if len(args) != 2 {
			return errors.New(usage)
		}
		return runLoad(args[1])
	case "roundtrip":
		if len(args) != 3 {
			return errors.New(usage)
		}
		size, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid chunk size: %w", err)
		}
		return runRoundTrip(args[2], size)
	default:
		return errors.New(usage)
	}
}

func runSplit(payload string, size int) error {
	chunks := splitPayload([]byte(payload), size)
	merged := string(pack.Join(asChunkBytes(chunks)))
	fmt.Printf("chunks=%d merged=%q\n", len(chunks), merged)
	return nil
}

func verifyRoundTrip(payload string, chunks []Chunk) error {
	manifest := Manifest{Kind: "roundtrip", TotalSize: len(payload), Chunks: chunks}
	merged := string(manifest.Reassemble())
	if merged != payload {
		return fmt.Errorf("round trip mismatch: got %q, want %q", merged, payload)
	}
	return nil
}

func roundTripWithPrint(payload string, chunks []Chunk) error {
	err := verifyRoundTrip(payload, chunks)
	if err == nil {
		fmt.Printf("roundtrip ok: chunks=%d payload=%q\n", len(chunks), payload)
	}
	return err
}

func runRoundTrip(payload string, size int) error {
	chunks := splitPayload([]byte(payload), size)
	return roundTripWithPrint(payload, chunks)
}

func saveManifest(rawPath string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(rawPath), 0o755); err != nil {
		return err
	}
	return pack.SaveWith(rawPath, manifest, jsoncodec.New[Manifest]())
}

func loadManifest(rawPath string) (Manifest, error) {
	return pack.LoadWith(rawPath, jsoncodec.New[Manifest]())
}

func saveAndLoad(rawPath string, manifest Manifest) (Manifest, error) {
	if err := saveManifest(rawPath, manifest); err != nil {
		return Manifest{}, err
	}
	return loadManifest(rawPath)
}

func runSave(rawPath, kind, owner, payload string) error {
	manifest := Manifest{Kind: kind, Owner: owner}
	if payload != "" {
		manifest.Chunks = splitPayload([]byte(payload), 8)
		manifest.TotalSize = len(payload)
	}
	loaded, err := saveAndLoad(rawPath, manifest)
	if err != nil {
		return err
	}
	fmt.Printf("saved=%s kind=%s owner=%s chunks=%d\n", rawPath, loaded.Kind, loaded.Owner, len(loaded.Chunks))
	return nil
}

func runLoad(rawPath string) error {
	loaded, err := loadManifest(rawPath)
	if err != nil {
		return err
	}
	fmt.Printf("kind=%s owner=%s chunks=%d total=%d\n", loaded.Kind, loaded.Owner, len(loaded.Chunks), loaded.TotalSize)
	return nil
}
