package cas_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	packfs "github.com/dmundt/go-cask/cas/backend/packfs"
)

// TestCapabilitiesOfFS pins fs.Backend as implementing every optional
// maintenance interface: it exposes Clean, Size and ModTime with the exact
// signatures Cleaner/Statter require.
func TestCapabilitiesOfFS(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := cas.CapabilitiesOf(backend)
	want := cas.Capabilities{Verify: true, Sweep: true, Clean: true, Stat: true}
	if got != want {
		t.Fatalf("CapabilitiesOf(fs) = %+v, want %+v", got, want)
	}
	var _ cas.Cleaner = backend
	var _ cas.Statter = backend
}

// TestCapabilitiesOfMem pins backmem.Backend as the minimal-interface reference:
// it implements neither Cleaner nor Statter, so VerifyAll/Sweep (which need
// only List/Get/Delete) are its only supported maintenance operations.
func TestCapabilitiesOfMem(t *testing.T) {
	got := cas.CapabilitiesOf(backmem.New())
	want := cas.Capabilities{Verify: true, Sweep: true, Clean: false, Stat: false}
	if got != want {
		t.Fatalf("CapabilitiesOf(mem) = %+v, want %+v", got, want)
	}
}

// TestCapabilitiesOfPackfs pins packfs.Backend as implementing the optional
// maintenance interfaces: it keeps scratch state in the pack directory and can
// report pack-file metadata for age-based sweep retention.
func TestCapabilitiesOfPackfs(t *testing.T) {
	backend, err := packfs.New(t.TempDir(), packfs.WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	got := cas.CapabilitiesOf(backend)
	want := cas.Capabilities{Verify: true, Sweep: true, Clean: true, Stat: true}
	if got != want {
		t.Fatalf("CapabilitiesOf(packfs) = %+v, want %+v", got, want)
	}
	var _ cas.Cleaner = backend
	var _ cas.Statter = backend
}
