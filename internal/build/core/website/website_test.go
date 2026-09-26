package website

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writePage creates a page under a temporary website root.
func writePage(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// scratchRoot makes an empty directory for materialized blocks.
func scratchRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "examples")
}

func TestGoBlocks(t *testing.T) {
	t.Parallel()

	content := "# Page\n\n" +
		"```go\npackage main\n\nfunc main() {}\n```\n\n" +
		"prose\n\n" +
		"```text\nnot go\n```\n\n" +
		"~~~go\npackage other\n~~~\n"

	blocks := GoBlocks("a.md", content)
	if len(blocks) != 3 {
		t.Fatalf("GoBlocks found %d blocks, want 3: %+v", len(blocks), blocks)
	}
	if blocks[0].Line != 3 || blocks[0].Info != "go" {
		t.Errorf("first block = line %d info %q, want line 3 info \"go\"", blocks[0].Line, blocks[0].Info)
	}
	if got := strings.Join(blocks[0].Body, "\n"); got != "package main\n\nfunc main() {}" {
		t.Errorf("first block body = %q", got)
	}
	if blocks[1].Info != "text" {
		t.Errorf("second block info = %q, want \"text\"", blocks[1].Info)
	}
	if blocks[2].Info != "go" {
		t.Errorf("tilde fence info = %q, want \"go\"", blocks[2].Info)
	}
}

func TestBlockDir(t *testing.T) {
	t.Parallel()

	block := Block{Page: "concepts/codecs.md", Line: 12, Info: "go"}
	if got := block.Dir(2); got != "concepts-codecs-2" {
		t.Errorf("Block.Dir(2) = %q, want %q", got, "concepts-codecs-2")
	}
}

// TestMaterialize pins the extraction: complete units are written one directory
// per block, and a fragment or a multi-info Go fence is reported instead.
func TestMaterialize(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePage(t, root, "a.md", "# A\n\n```go\npackage main\n\nfunc main() {}\n```\n")
	writePage(t, root, "sub/b.md", "# B\n\n```go\npackage sub\n\nvar X = 1\n```\n\n```go\nx := 1\n```\n")
	writePage(t, root, "c.md", "# C\n\n```text\nnot go\n```\n\n```go extra\npackage c\n```\n")

	scratch := scratchRoot(t)
	pages, written, findings := Materialize(root, scratch)

	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
	if len(written) != 2 {
		t.Errorf("written = %d blocks, want 2: %+v", len(written), written)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %q, want 2 (a fragment and a multi-info fence)", findings)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "sub/b.md:9: Go block is not a complete unit") {
		t.Errorf("findings = %q, want the fragment on sub/b.md line 9", findings)
	}
	if !strings.Contains(joined, `c.md:7: a Go fence must use the info string "go" alone, not "go extra"`) {
		t.Errorf("findings = %q, want the info-string error for c.md line 7", findings)
	}
	if !sort.StringsAreSorted(findings) {
		t.Errorf("findings are not sorted, so two runs could log them differently: %q", findings)
	}

	// One directory per materialized block, each holding example.go. There are two
	// materialized pages, so each contributes its first (and only) block.
	for _, block := range written {
		dir := filepath.Join(scratch, block.Dir(1))
		data, err := os.ReadFile(filepath.Join(dir, "example.go"))
		if err != nil {
			t.Errorf("materialized block from %s: %v", block.Page, err)
			continue
		}
		if !strings.HasPrefix(string(data), "package ") {
			t.Errorf("materialized block from %s does not start with a package clause: %q", block.Page, data)
		}
	}
}

// TestMaterializeClearsScratch pins that a stale copy cannot survive: a block
// removed from a page must not keep being built.
func TestMaterializeClearsScratch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePage(t, root, "a.md", "# A\n\n```go\npackage main\n```\n")
	scratch := scratchRoot(t)

	if _, _, findings := Materialize(root, scratch); len(findings) != 0 {
		t.Fatalf("unexpected findings: %q", findings)
	}
	stale := filepath.Join(scratch, "stale", "example.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("package stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, findings := Materialize(root, scratch); len(findings) != 0 {
		t.Fatalf("unexpected findings on the second run: %q", findings)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a stale materialized block survived a re-run (stat error: %v)", err)
	}
}

func TestDocumentedPackages(t *testing.T) {
	t.Parallel()

	content := "# Codecs\n\n" +
		"| Package | What |\n|---|---|\n" +
		"| `cas/codec/json` | JSON |\n" +
		"| `cas/codec/gob` and `cas/codec/cbor` | others |\n\n" +
		"Prose mentions `cas/codec/notatable` but is not a table row.\n"

	got := DocumentedPackages(content, "cas/codec")
	want := []string{"cas/codec/cbor", "cas/codec/gob", "cas/codec/json"}
	if len(got) != len(want) {
		t.Fatalf("DocumentedPackages = %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("DocumentedPackages = %q, want %q", got, want)
		}
	}
}

func TestShippedPackages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile := func(name string) {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("cas/codec/json/codec.go")
	writeFile("cas/codec/gob/codec.go")
	// A directory with no Go source is not a shipped package.
	writeFile("cas/codec/testdata/notes.txt")
	// A file directly under the root is not a package.
	writeFile("cas/codec/README.md")

	got, err := ShippedPackages(root, "cas/codec")
	if err != nil {
		t.Fatalf("ShippedPackages: %v", err)
	}
	if len(got) != 2 || !got["cas/codec/json"] || !got["cas/codec/gob"] {
		t.Errorf("ShippedPackages = %v, want exactly json and gob", got)
	}
}

// TestCheckInventory pins both directions: an undocumented package and a row for
// a package that no longer exists.
func TestCheckInventory(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	web := t.TempDir()
	for _, name := range []string{"cas/backend/fs/fs.go", "cas/backend/mem/mem.go", "cas/backend/new/new.go"} {
		full := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePage(t, web, "concepts/backends.md",
		"| Package | What |\n|---|---|\n| `cas/backend/fs` | fs |\n| `cas/backend/ghost` | gone |\n")

	findings := CheckInventory(repo, web, Inventory{Page: "concepts/backends.md", Root: "cas/backend"})
	if len(findings) != 2 {
		t.Fatalf("CheckInventory = %q, want 2 findings", findings)
	}
	if !strings.Contains(findings[0], "missing cas/backend/mem, cas/backend/new") {
		t.Errorf("first finding = %q, want the two undocumented packages", findings[0])
	}
	if !strings.Contains(findings[1], "cas/backend/ghost") {
		t.Errorf("second finding = %q, want the stale row", findings[1])
	}
}

// TestCheckInventoryMissingPage pins that an absent page is reported rather than
// treated as an empty, satisfied table.
func TestCheckInventoryMissingPage(t *testing.T) {
	t.Parallel()

	findings := CheckInventory(t.TempDir(), t.TempDir(), Inventory{Page: "concepts/backends.md", Root: "cas/backend"})
	if len(findings) != 1 || !strings.Contains(findings[0], "inventory page is missing") {
		t.Fatalf("CheckInventory = %q, want a missing-page finding", findings)
	}
}
