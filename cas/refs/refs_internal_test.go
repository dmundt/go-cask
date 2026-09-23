package refs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/hash/sha256"
)

func TestWriteFileAtomicReplacesStaleTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main")
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("fresh\n")); err != nil {
		t.Fatalf("writeFileAtomic = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fresh\n" {
		t.Fatalf("writeFileAtomic wrote %q, want %q", string(data), "fresh\n")
	}
}

func TestParseLogIgnoresMalformedTail(t *testing.T) {
	d1 := sha256.Of([]byte("a"))
	d2 := sha256.Of([]byte("b"))
	data := []byte("bad\n" +
		"1\t" + d1.String() + "\t\n\n" +
		"2\t" + d2.String() + "\t" + d1.String() + "\n")
	entries := parseLog(data)
	if len(entries) != 2 {
		t.Fatalf("parseLog = %d entries, want 2", len(entries))
	}
	if !entries[0].Digest.Equal(d1) || !entries[0].Old.IsZero() {
		t.Fatalf("parseLog[0] = %+v, want Digest=%s Old=absent", entries[0], d1)
	}
	if !entries[1].Digest.Equal(d2) || !entries[1].Old.Equal(d1) {
		t.Fatalf("parseLog[1] = %+v, want Digest=%s Old=%s", entries[1], d2, d1)
	}
}

func TestResolveEmptyNameAndRootsSkipZero(t *testing.T) {
	ctx := context.Background()
	s := &Store{dir: t.TempDir(), now: time.Now}
	if _, err := s.Resolve(ctx, ""); err == nil {
		t.Fatal("Resolve(empty) = nil error, want error")
	}
	if err := s.Set(ctx, "a", sha256.Of([]byte("a"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "b", nil); err == nil {
		t.Fatal("Set(nil digest) = nil error, want error")
	}
	roots, err := s.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || !roots[0].Equal(sha256.Of([]byte("a"))) {
		t.Fatalf("Roots = %v, want [%s]", roots, sha256.Of([]byte("a")))
	}
}

func TestValidateNameRejectsWindowsReservedNamesAndBackslash(t *testing.T) {
	for _, name := range []string{"con", "CON.txt", "a\\b", "a/b\\c"} {
		if err := ValidateName(name); err == nil {
			t.Fatalf("ValidateName(%q) = nil, want error", name)
		}
	}
	if _, err := cas.ParseDigest(""); err == nil {
		t.Fatal("ParseDigest(empty) = nil error, want error")
	}
}

func TestListMissingDirAndCorruptValueAreHandled(t *testing.T) {
	ctx := context.Background()
	missing := &Store{dir: filepath.Join(t.TempDir(), "missing"), now: time.Now}
	if got, err := missing.List(ctx); err != nil || got != nil {
		t.Fatalf("List(missing dir) = (%v, %v), want (nil, nil)", got, err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main")
	if err := os.WriteFile(path, []byte("not-a-digest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &Store{dir: dir, now: time.Now}
	if _, err := st.readValue("main"); err == nil {
		t.Fatal("readValue(corrupt digest) = nil error, want error")
	}
}
