package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	packfs "github.com/dmundt/go-cask/cas/backend/packfs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/verify/adler32"
	"github.com/dmundt/go-cask/cas/verify/crc32"
	"github.com/dmundt/go-cask/cas/verify/sidecar"
)

// runBoth is run() with stderr captured too, so a message an operator reads on
// the error stream can be asserted (cli §3).
func runBoth(t *testing.T, mf modeFlags, cmd string, args ...string) (string, string, int) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	code := runOp(context.Background(), mf, cmd, args)
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var out, errOut bytes.Buffer
	if _, err := out.ReadFrom(outR); err != nil {
		t.Fatal(err)
	}
	if _, err := errOut.ReadFrom(errR); err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// storedWithChecksum stores data through a checksum-recording decorator over the
// fs store at path, so a CLI test starts from a store that has a record.
func storedWithChecksum(t *testing.T, path string, data []byte) cas.Digest {
	t.Helper()
	backend, err := fs.New(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := sidecar.New(backend, sidecar.WithChecksum(crc32.Name, crc32.New()))
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Of(data)
	if err := rec.Put(context.Background(), d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	return d
}

// packedWithChecksum is storedWithChecksum for the packfile backend.
func packedWithChecksum(t *testing.T, path string, data []byte) cas.Digest {
	t.Helper()
	backend, err := packfs.New(path, packfs.WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	// The packfile backend holds its active pack open, which Windows refuses to
	// unlink at temp-directory cleanup.
	t.Cleanup(func() { _ = backend.Close() })
	rec, err := sidecar.New(backend, sidecar.WithChecksum(crc32.Name, crc32.New()))
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Of(data)
	if err := rec.Put(context.Background(), d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	return d
}

// fsObjectPath is the fs backend's default fan-out path for d.
func fsObjectPath(store string, d cas.Digest) string {
	return filepath.Join(store, d.String()[:2], d.String())
}

// recordFile is the sidecar record file for d under store/objects.
func recordFile(store string, d cas.Digest) string {
	return filepath.Join(store, ".meta", d.String()+".json")
}

func TestVerifyChecksumsSingleObject(t *testing.T) {
	mf := localMF(t)
	d := storedWithChecksum(t, mf.store, []byte("recorded payload"))

	out, code := run(t, mf, "verify", "--checksums", sha256.Format(d))
	if code != 0 {
		t.Fatalf("verify --checksums exit %d, want 0 (%s)", code, out)
	}
	if !strings.Contains(out, "crc32 checksum ok") {
		t.Errorf("verify --checksums output = %q, want a checksum-ok line", out)
	}
}

func TestVerifyChecksumsUnrecordedIsNotCorruption(t *testing.T) {
	mf := localMF(t)
	out, code := run(t, mf, "put", writeTemp(t, "no record for this one"))
	if code != 0 {
		t.Fatal("put failed")
	}
	h := strings.TrimSpace(out)

	out, code = run(t, mf, "verify", "--checksums", h)
	if code != 0 {
		t.Fatalf("verify --checksums on an unrecorded object exit %d, want 0 (%s)", code, out)
	}
	if !strings.Contains(out, "no crc32 checksum record") {
		t.Errorf("output = %q, want it to say the object has no record", out)
	}

	out, code = run(t, mf, "verify", "--checksums", "--all")
	if code != 0 {
		t.Fatalf("verify --checksums --all exit %d, want 0 (%s)", code, out)
	}
	if !strings.Contains(out, "checked 0 recorded objects, 0 corrupt, 1 unrecorded") {
		t.Errorf("summary = %q, want 0 recorded and 1 unrecorded", out)
	}
}

func TestVerifyChecksumsReportsMismatch(t *testing.T) {
	mf := localMF(t)
	data := []byte("tamper with this payload")
	d := storedWithChecksum(t, mf.store, data)
	// Same length, so the size check passes and the checksum is what disagrees.
	if err := os.WriteFile(fsObjectPath(mf.store, d), bytes.Repeat([]byte("x"), len(data)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runBoth(t, mf, "verify", "--checksums", sha256.Format(d))
	if code != 1 {
		t.Fatalf("verify --checksums on tampered bytes exit %d, want 1", code)
	}
	// The line must be distinguishable from the address CORRUPT line.
	if !strings.Contains(errOut, "CHECKSUM MISMATCH") {
		t.Errorf("stderr = %q, want a CHECKSUM MISMATCH line", errOut)
	}
	if strings.Contains(errOut, "CORRUPT") {
		t.Errorf("stderr = %q, must not reuse the address CORRUPT label", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing on a mismatch", out)
	}

	out, _, code = runBoth(t, mf, "verify", "--checksums", "--all")
	if code != 1 {
		t.Fatalf("verify --checksums --all exit %d, want 1", code)
	}
	if !strings.Contains(out, "checked 1 recorded objects, 1 corrupt, 0 unrecorded") {
		t.Errorf("summary = %q, want one corrupt recorded object", out)
	}
}

func TestVerifyChecksumsWrongAlgorithmIsAnError(t *testing.T) {
	mf := localMF(t)
	d := storedWithChecksum(t, mf.store, []byte("crc32 record, adler32 read"))
	out, errOut, code := runBoth(t, mf, "verify", "--checksums", "--checksum", adler32.Name, sha256.Format(d))
	if code != 1 {
		t.Fatalf("verify --checksums --checksum adler32 exit %d, want 1 (%s)", code, out)
	}
	if !strings.Contains(errOut, sidecar.ErrChecksumAlgorithm.Error()) {
		t.Errorf("stderr = %q, want it to name the algorithm mismatch", errOut)
	}
}

func TestVerifyChecksumsFlagUsage(t *testing.T) {
	mf := localMF(t)
	cases := [][]string{
		{"verify", "--checksum", crc32.Name, "sha256:" + strings.Repeat("ab", 32)},
		{"verify", "--checksums", "--checksum", "bogus", "sha256:" + strings.Repeat("ab", 32)},
		{"verify", "--checksums"},
		{"verify", "--checksums", "--all", "extra"},
	}
	for _, args := range cases {
		if _, code := run(t, mf, args[0], args[1:]...); code != 2 {
			t.Errorf("%v exit = %d, want 2 (usage)", args, code)
		}
	}
}

func TestVerifyChecksumsOverPackfs(t *testing.T) {
	// "packfs" is the -backend value store.ParseKind accepts (cli §4).
	mf := modeFlags{store: t.TempDir(), backend: "packfs"}
	d := packedWithChecksum(t, mf.store, []byte("packed recorded payload"))
	out, code := run(t, mf, "verify", "--checksums", sha256.Format(d))
	if code != 0 {
		t.Fatalf("verify --checksums over packfs exit %d, want 0 (%s)", code, out)
	}
	if !strings.Contains(out, "crc32 checksum ok") {
		t.Errorf("output = %q, want a checksum-ok line", out)
	}
	out, code = run(t, mf, "verify", "--checksums", "--all")
	if code != 0 || !strings.Contains(out, "checked 1 recorded objects, 0 corrupt, 0 unrecorded") {
		t.Fatalf("verify --checksums --all over packfs = (%q, %d)", out, code)
	}
}

func TestGcReconcilesChecksumRecords(t *testing.T) {
	mf := localMF(t)
	kept := storedWithChecksum(t, mf.store, []byte("kept object"))
	doomed := storedWithChecksum(t, mf.store, []byte("doomed object"))

	out, code := run(t, mf, "gc", "--min-age", "0", sha256.Format(kept))
	if code != 0 {
		t.Fatalf("gc exit %d (%s)", code, out)
	}
	if !strings.Contains(out, "checksum records: 2 examined, 1 orphaned removed, 0 objects unrecorded") {
		t.Errorf("gc output = %q, want the reconciliation summary", out)
	}
	if _, err := os.Stat(recordFile(mf.store, doomed)); !os.IsNotExist(err) {
		t.Errorf("the swept object's record survived gc: %v", err)
	}
	if _, err := os.Stat(recordFile(mf.store, kept)); err != nil {
		t.Errorf("gc removed the surviving object's record: %v", err)
	}
}

func TestPruneReconcilesOnlyWhenItDeletes(t *testing.T) {
	mf := localMF(t)
	kept := storedWithChecksum(t, mf.store, []byte("kept object"))
	doomed := storedWithChecksum(t, mf.store, []byte("doomed object"))

	out, code := run(t, mf, "prune", "--min-age", "0", sha256.Format(kept))
	if code != 0 {
		t.Fatalf("prune (dry-run) exit %d (%s)", code, out)
	}
	if strings.Contains(out, "checksum records") {
		t.Errorf("a dry run reconciled records: %q", out)
	}
	if _, err := os.Stat(recordFile(mf.store, doomed)); err != nil {
		t.Errorf("a dry run removed a record: %v", err)
	}

	out, code = run(t, mf, "prune", "--min-age", "0", "--dry-run=false", sha256.Format(kept))
	if code != 0 {
		t.Fatalf("prune exit %d (%s)", code, out)
	}
	if !strings.Contains(out, "checksum records: 2 examined, 1 orphaned removed") {
		t.Errorf("prune output = %q, want the reconciliation summary", out)
	}
	if _, err := os.Stat(recordFile(mf.store, doomed)); !os.IsNotExist(err) {
		t.Errorf("the swept object's record survived prune: %v", err)
	}
}
