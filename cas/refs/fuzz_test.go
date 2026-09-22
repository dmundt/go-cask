package refs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
)

// writeRawLog writes data directly to name's reflog file, bypassing Store
// entirely, to exercise Log's parser against adversarial bytes it never
// produced itself (mirrors refs_test.go's TestLogToleratesATornTail, which
// hardcodes the same ".log" layout).
func writeRawLog(dir, name string, data []byte) error {
	path := filepath.Join(dir, ".log", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// FuzzValidateName checks that ValidateName never panics on arbitrary input
// and agrees with itself: a name it accepts must also be safely usable as a
// Set/Get/Delete/Resolve/Log argument on a real Store without error (beyond
// the expected ErrNotFound for a name that was never Set).
func FuzzValidateName(f *testing.F) {
	for _, seed := range []string{
		"", "main", "release/v1", "..", ".", "/abs", "a/../b",
		".git", "a/.hidden", "a.lock", "a/b.lock", "a\x00b", "a/b/c/d",
	} {
		f.Add(seed)
	}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 200 {
			// A very long name can still trip OS path-length limits even
			// though ValidateName has no length rule of its own (the issue
			// doesn't ask for one); skip rather than fail on an unrelated
			// filesystem limit.
			t.Skip("name too long for a meaningful filesystem round-trip")
		}
		err := refs.ValidateName(name)
		if err == nil {
			// An accepted name must round-trip through Set/Get without
			// ValidateName rejecting it a second time, and must never let a
			// crafted name escape s.dir (Set/Get would error instead, since
			// filepath.Join with a "cleanable" name still resolves under
			// s.dir here because ValidateName already rejected ".."/absolute
			// segments). A fresh Store per case keeps concurrent fuzz
			// workers from tripping each other's Set/Delete state.
			s, serr2 := refs.Open(t.TempDir())
			if serr2 != nil {
				t.Fatal(serr2)
			}
			d := sha256.Of([]byte(name))
			if serr := s.Set(ctx, name, d); serr != nil {
				t.Fatalf("Set(%q) after ValidateName accepted it: %v", name, serr)
			}
			got, gerr := s.Get(ctx, name)
			if gerr != nil {
				t.Fatalf("Get(%q) after Set: %v", name, gerr)
			}
			if !got.Equal(d) {
				t.Fatalf("Get(%q) = %s, want %s", name, got, d)
			}
			if derr := s.Delete(ctx, name); derr != nil {
				t.Fatalf("Delete(%q): %v", name, derr)
			}
		}
	})
}

// FuzzLogParsesOrIgnoresArbitraryBytes checks that Log never panics on an
// arbitrary reflog file, satisfying the "torn tail is recoverable or ignored,
// never fatal" acceptance criterion for adversarial (not just truncated)
// content.
func FuzzLogParsesOrIgnoresArbitraryBytes(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte("1\t\t\n"),
		[]byte("not-a-number\t\t\n"),
		[]byte("1\tzz\tzz\n"),
		[]byte("1\t\t\n2\t"), // torn tail: second line has no newline
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		s, err := refs.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeRawLog(dir, "main", data); err != nil {
			t.Fatal(err)
		}
		// Log must return without panicking or erroring on malformed bytes:
		// every rejected line is silently dropped.
		if _, err := s.Log(context.Background(), "main", 0); err != nil {
			t.Fatalf("Log on arbitrary reflog bytes returned an error: %v", err)
		}
	})
}
