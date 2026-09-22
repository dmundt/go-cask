package refs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
)

func digest(s string) cas.Digest { return sha256.Of([]byte(s)) }

func mustOpen(t *testing.T, opts ...refs.Option) *refs.Store {
	t.Helper()
	s, err := refs.Open(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := refs.Open(""); err == nil {
		t.Fatal("Open(\"\") = nil error, want error")
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	d := digest("v1")
	if err := s.Set(ctx, "main", d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(d) {
		t.Fatalf("Get = %s, want %s", got, d)
	}
}

func TestGetUnknownNameIsNotFound(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("Get(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestSetIsAtomicNoPartialFileSurvivesACrashedWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := refs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := digest("v1")
	if err := s.Set(ctx, "main", d); err != nil {
		t.Fatal(err)
	}
	// Simulate a writer that crashed after creating its temp file but before
	// the rename: List/Get must never see (or be confused by) the leftover.
	if err := os.WriteFile(filepath.Join(dir, "main.tmp"), []byte("garbage, no newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(d) {
		t.Fatalf("Get after crashed writer left a .tmp = %s, want the old value %s (never a partial read)", got, d)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "main" {
		t.Fatalf("List = %v, want only [main] (the leftover .tmp must not appear as a ref)", all)
	}
}

func TestSetAppendsReflogEntry(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	v1, v2 := digest("v1"), digest("v2")
	if err := s.Set(ctx, "main", v1); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "main", v2); err != nil {
		t.Fatal(err)
	}
	log, err := s.Log(ctx, "main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("Log = %d entries, want 2: %v", len(log), log)
	}
	// Newest first.
	if !log[0].Digest.Equal(v2) || !log[0].Old.Equal(v1) {
		t.Errorf("log[0] = %+v, want Digest=v2 Old=v1", log[0])
	}
	if !log[1].Digest.Equal(v1) || !log[1].Old.IsZero() {
		t.Errorf("log[1] = %+v, want Digest=v1 Old=absent", log[1])
	}
}

func TestLogRespectsLimit(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	for i := 0; i < 5; i++ {
		if err := s.Set(ctx, "main", digest(string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	log, err := s.Log(ctx, "main", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("Log(limit=2) = %d entries, want 2", len(log))
	}
	last, err := s.Get(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !log[0].Digest.Equal(last) {
		t.Fatalf("Log(limit=2)[0] = %s, want current value %s", log[0].Digest, last)
	}
}

func TestLogUnknownNameIsEmptyNotError(t *testing.T) {
	log, err := (mustOpen(t)).Log(context.Background(), "never-set", 0)
	if err != nil {
		t.Fatal(err)
	}
	if log != nil {
		t.Fatalf("Log(never-set) = %v, want nil", log)
	}
}

func TestLogToleratesATornTail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := refs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1 := digest("v1")
	if err := s.Set(ctx, "main", v1); err != nil {
		t.Fatal(err)
	}
	// Append a truncated, mid-write line (a crash mid-append), no fields, no
	// trailing newline.
	logPath := filepath.Join(dir, ".log", "main")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("17000000\tbad-part"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := s.Log(ctx, "main", 0)
	if err != nil {
		t.Fatalf("Log with a torn tail must not fail: %v", err)
	}
	if len(log) != 1 || !log[0].Digest.Equal(v1) {
		t.Fatalf("Log with a torn tail = %v, want the one complete entry", log)
	}
}

func TestPreviousReturnsPriorValue(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	v1, v2 := digest("v1"), digest("v2")
	if err := s.Set(ctx, "main", v1); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "main", v2); err != nil {
		t.Fatal(err)
	}
	prev, err := s.Previous(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !prev.Equal(v1) {
		t.Fatalf("Previous = %s, want %s", prev, v1)
	}
}

func TestPreviousOnFirstSetIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	if err := s.Set(ctx, "main", digest("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Previous(ctx, "main"); !errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("Previous after one Set error = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesValueButKeepsLog(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	v1 := digest("v1")
	if err := s.Set(ctx, "main", v1); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "main"); !errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	log, err := s.Log(ctx, "main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || !log[0].Digest.IsZero() || !log[0].Old.Equal(v1) {
		t.Fatalf("Log after Delete = %v, want a tombstone entry (Digest absent, Old=v1) first", log)
	}
}

func TestDeleteUnknownNameIsNoop(t *testing.T) {
	if err := (mustOpen(t)).Delete(context.Background(), "never-set"); err != nil {
		t.Fatalf("Delete(never-set) = %v, want nil (idempotent no-op)", err)
	}
}

func TestListSortedAndEmptyStoreReturnsNil(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	all, err := s.List(ctx)
	if err != nil || all != nil {
		t.Fatalf("List(empty) = %v, %v; want nil, nil", all, err)
	}
	names := []string{"z", "a", "release/v2", "release/v1"}
	for _, n := range names {
		if err := s.Set(ctx, n, digest(n)); err != nil {
			t.Fatal(err)
		}
	}
	all, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(names) {
		t.Fatalf("List = %d refs, want %d: %v", len(all), len(names), all)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name >= all[i].Name {
			t.Fatalf("List not sorted: %v", all)
		}
	}
}

func TestResolveExactNameWinsOverPrefix(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	if err := s.Set(ctx, "release", digest("release")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "release-2", digest("release-2")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(ctx, "release")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "release" {
		t.Fatalf("Resolve(exact) = %q, want %q", r.Name, "release")
	}
}

func TestResolveUniquePrefix(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	if err := s.Set(ctx, "release/v1", digest("v1")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(ctx, "release")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "release/v1" {
		t.Fatalf("Resolve(unique prefix) = %q, want %q", r.Name, "release/v1")
	}
}

func TestResolveAmbiguousPrefixNamesCandidates(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	if err := s.Set(ctx, "release/v1", digest("v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "release/v2", digest("v2")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Resolve(ctx, "release")
	if !errors.Is(err, refs.ErrAmbiguous) {
		t.Fatalf("Resolve(ambiguous) error = %v, want ErrAmbiguous", err)
	}
	if !strings.Contains(err.Error(), "release/v1") || !strings.Contains(err.Error(), "release/v2") {
		t.Fatalf("Resolve(ambiguous) error %q must name both candidates", err.Error())
	}
}

func TestResolveUnknownIsNotFound(t *testing.T) {
	if _, err := (mustOpen(t)).Resolve(context.Background(), "nope"); !errors.Is(err, refs.ErrNotFound) {
		t.Fatalf("Resolve(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestRootsReturnsEveryCurrentDigest(t *testing.T) {
	ctx := context.Background()
	s := mustOpen(t)
	a, b := digest("a"), digest("b")
	if err := s.Set(ctx, "a", a); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "b", b); err != nil {
		t.Fatal(err)
	}
	roots, err := s.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("Roots = %v, want 2 digests", roots)
	}
}

func TestValidateNameRejectsUnsafeNames(t *testing.T) {
	bad := []string{
		"",
		"/abs",
		"..",
		"a/../b",
		"a/./b",
		".hidden",
		"a/.hidden",
		"foo.lock",
		"a/foo.lock",
		"has\x00nul",
		" ",
		"trailing ",
		"a/trailing ",
		"trailing.",
		"a/trailing.",
	}
	for _, name := range bad {
		if err := refs.ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", name)
		}
	}
}

func TestValidateNameAcceptsOrdinaryNames(t *testing.T) {
	good := []string{"main", "release/v1", "a/b/c", "feature-x"}
	for _, name := range good {
		if err := refs.ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestSetSetsAndRejectsAbsentDigest(t *testing.T) {
	if err := (mustOpen(t)).Set(context.Background(), "main", nil); err == nil {
		t.Fatal("Set(absent digest) = nil error, want error")
	}
}

func TestSetRejectsInvalidName(t *testing.T) {
	if err := (mustOpen(t)).Set(context.Background(), "..", digest("v1")); !errors.Is(err, refs.ErrInvalidName) {
		t.Fatalf("Set(bad name) error = %v, want ErrInvalidName", err)
	}
}

func TestWithClockControlsReflogTimestamps(t *testing.T) {
	ctx := context.Background()
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	s := mustOpen(t, refs.WithClock(func() time.Time { return fixed }))
	if err := s.Set(ctx, "main", digest("v1")); err != nil {
		t.Fatal(err)
	}
	log, err := s.Log(ctx, "main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || !log[0].Time.Equal(fixed) {
		t.Fatalf("Log[0].Time = %v, want %v", log[0].Time, fixed)
	}
}

func TestContextCancellationIsHonored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := mustOpen(t)
	if err := s.Set(ctx, "main", digest("v1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set(canceled ctx) = %v, want context.Canceled", err)
	}
	if _, err := s.Get(ctx, "main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get(canceled ctx) = %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(canceled ctx) = %v, want context.Canceled", err)
	}
}
