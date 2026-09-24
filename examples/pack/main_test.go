package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPackExampleCommands(t *testing.T) {
	t.Run("split", func(t *testing.T) {
		if err := run(context.Background(), []string{"split", "8", "hello world"}); err != nil {
			t.Fatalf("split command failed: %v", err)
		}
	})

	t.Run("roundtrip", func(t *testing.T) {
		if err := run(context.Background(), []string{"roundtrip", "8", "hello world"}); err != nil {
			t.Fatalf("roundtrip command failed: %v", err)
		}
	})

	t.Run("save and load", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.json")
		if err := run(context.Background(), []string{"save", path, "artifact", "alice"}); err != nil {
			t.Fatalf("save command failed: %v", err)
		}
		if err := run(context.Background(), []string{"load", path}); err != nil {
			t.Fatalf("load command failed: %v", err)
		}
	})

	t.Run("save with payload", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "payload.json")
		if err := run(context.Background(), []string{"save", path, "artifact", "alice", "hello world"}); err != nil {
			t.Fatalf("save with payload failed: %v", err)
		}
	})

	t.Run("typed manifest round trip", func(t *testing.T) {
		payload := "hello world"
		chunks := splitPayload([]byte(payload), 8)
		manifest := Manifest{Kind: "artifact", Owner: "alice", Chunks: chunks, TotalSize: len(payload)}
		if got := string(manifest.Reassemble()); got != payload {
			t.Fatalf("manifest reassemble mismatch: got %q want %q", got, payload)
		}
		if got := (Manifest{}).Reassemble(); got != nil {
			t.Fatalf("empty manifest should reassemble to nil, got %q", got)
		}
		if err := verifyRoundTrip("hello", []Chunk{{Data: "goodbye"}}); err == nil {
			t.Fatal("verifyRoundTrip should fail on mismatched chunk data")
		}
		parentFile := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(parentFile, []byte("x"), 0o644); err != nil {
			t.Fatalf("prepare file parent: %v", err)
		}
		if err := saveManifest(context.Background(), filepath.Join(parentFile, "manifest.json"), Manifest{Kind: "artifact"}); err == nil {
			t.Fatal("saveManifest should fail when parent is a file")
		}
		if err := roundTripWithPrint("hello", []Chunk{{Data: "goodbye"}}); err == nil {
			t.Fatal("roundTripWithPrint should fail on mismatched chunk data")
		}
		if _, err := loadManifest(context.Background(), "bad\x00path"); err == nil {
			t.Fatal("loadManifest should fail on invalid path")
		}
		if _, err := saveAndLoad(context.Background(), "bad\x00path", Manifest{Kind: "artifact"}); err == nil {
			t.Fatal("saveAndLoad should fail on invalid path")
		}
	})

	t.Run("invalid usage", func(t *testing.T) {
		if err := run(context.Background(), nil); err == nil {
			t.Fatal("expected usage error")
		}
		for _, args := range [][]string{{"bogus"}, {"split"}, {"split", "bad", "hello"}, {"roundtrip"}, {"roundtrip", "bad", "hello"}, {"save", "path", "kind"}, {"save", "path", "kind", "owner", "payload", "extra"}, {"load"}, {"load", "x", "extra"}, {"save", "bad\x00path", "kind", "owner"}, {"load", "bad\x00path"}} {
			if err := run(context.Background(), args); err == nil {
				t.Fatalf("expected error for args %#v", args)
			}
		}
	})
}

func TestPackExampleFileOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.json")
	if err := run(context.Background(), []string{"save", path, "artifact", "bob"}); err != nil {
		t.Fatalf("save command failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved manifest: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved manifest should not be empty")
	}
}

func TestPackMainEntryPoint(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"pack", "roundtrip", "8", "hello"}
	main()
}

func TestPackMainErrorPath(t *testing.T) {
	if os.Getenv("GO_WANT_PACK_HELPER_PROCESS") == "1" {
		oldArgs := os.Args
		defer func() { os.Args = oldArgs }()
		os.Args = []string{"pack"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestPackMainErrorPath")
	cmd.Env = append(os.Environ(), "GO_WANT_PACK_HELPER_PROCESS=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("main should exit with an error on invalid args")
	}
}
