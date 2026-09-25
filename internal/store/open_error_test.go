package store

// Two Open branches are left uncovered on purpose (testing-strategy §5):
//
//   - store.go:133, the packfs constructor's error return. fs.New and
//     packfs.New run the same fs.ValidateBase path arithmetic before touching
//     the filesystem, so every path that fails one fails the other; the KindFS
//     case above (TestOpenReportsAnUncreatableBackend) reaches the identical
//     branch shape through fs.New, and a second test would exercise the same
//     code path with a different backend name.
//
//   - store.go:141, the switch's default "unknown backend" return. ParseKind is
//     the switch's only source of kind, and it accepts exactly KindFS and
//     KindPackFS, so the default is unreachable for a caller that went through
//     it — it exists so the compiler checks that every case is named.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenReportsAnUncreatableBackend covers the branch between Open's option
// checks and its success path: a path that passes the empty-path check and then
// fails inside the selected backend is reported as that backend's error. A
// parent that is a regular file cannot hold the store directory, so
// os.MkdirAll in fs.New fails with ENOTDIR and the file is reported rather than
// a half-opened Store.
func TestOpenReportsAnUncreatableBackend(t *testing.T) {
	ctx := context.Background()
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parentFile, "objects")

	st, err := Open(ctx, Options{Kind: KindFS, Path: path})
	if err == nil {
		t.Fatalf("Open beneath a regular file = (%v, nil), want an error", st)
	}
	if st != nil {
		t.Fatalf("Open beneath a regular file returned %#v, want no store", st)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("the failed Open created %s", path)
	}
}
