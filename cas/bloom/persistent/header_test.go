package persistent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// Environment variables the cross-process test passes to its child.
const (
	childPathEnv  = "CASK_BLOOM_PERSISTENT_CHILD_PATH"
	childValueEnv = "CASK_BLOOM_PERSISTENT_CHILD_VALUE"
)

// TestFilterPersistentKeepsBitsAcrossProcesses is the regression guard for #254:
// the default index hash used to be seeded per process (`maphash.MakeSeed`)
// while only the bitset was persisted, so a filter reopened by the next process
// reported false for every digest the previous one had recorded — and
// bloom.Guard treats a negative as authoritative absence, which turns a lost
// hint into a wrong answer. The child process writes the filter; this process
// reopens it.
func TestFilterPersistentKeepsBitsAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cross-process.bin")
	const value = "written by the child process"

	cmd := exec.Command(os.Args[0], "-test.run=^TestFilterPersistentChildProcess$")
	cmd.Env = append(os.Environ(), childPathEnv+"="+path, childValueEnv+"="+value)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process failed: %v\n%s", err, out)
	}

	f, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !f.Contains(cas.NewDigest([]byte(value))) {
		t.Fatal("a filter reopened in another process reported an added digest as absent (#254)")
	}
}

// TestFilterPersistentChildProcess is the child half of
// TestFilterPersistentKeepsBitsAcrossProcesses. It skips in the parent run,
// where the environment variable is unset.
func TestFilterPersistentChildProcess(t *testing.T) {
	path := os.Getenv(childPathEnv)
	if path == "" {
		t.Skip("helper process for TestFilterPersistentKeepsBitsAcrossProcesses")
	}
	f, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add(cas.NewDigest([]byte(os.Getenv(childValueEnv))))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestFilterPersistentAdoptsLegacyBitsetFile covers the file written before the
// header existed: it carries no index key, so its bits cannot be indexed the way
// this reader indexes them. The file is adopted and rebuilt empty — a lost hint
// set, never a wrong answer — and stays usable from then on (#254).
func TestFilterPersistentAdoptsLegacyBitsetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.bin")
	// A raw bitset with every bit set is the shape of a pre-header file.
	if err := os.WriteFile(path, bytes.Repeat([]byte{0xff}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("added after the header existed"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("the rebuilt filter lost a digest it had just added")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, magic[:]) {
		t.Fatalf("the adopted file was not given a header: % x", raw[:min(len(raw), headerSize)])
	}
	kind, key, ok := decodeHeader(raw)
	if !ok || kind != hashKindDefault {
		t.Fatalf("adopted header = (kind %d, ok %v), want the default kind", kind, ok)
	}
	if key == (indexKey{}) {
		t.Fatal("adopted header carries no index key")
	}

	reopened, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.Contains(d) {
		t.Fatal("the adopted filter did not survive a reopen")
	}
}

// TestFilterPersistentCustomHashKind pins the two index-hash kinds: a
// caller-supplied hash is the caller's own determinism contract and its bits are
// reused on reopen, while reopening the same file under the default hash
// rebuilds it instead of answering from bits indexed by another rule (#254).
func TestFilterPersistentCustomHashKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.bin")
	custom := func(data []byte, i int) uint64 {
		h := uint64(14695981039346656037)
		for _, b := range data {
			h ^= uint64(b)
			h *= 1099511628211
		}
		return h ^ uint64(i)
	}
	cfg := Config{ExpectedItems: 256, FalsePositiveRate: 0.01, Hash: custom}

	f, err := NewFilter(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("custom-hashed"))
	f.Add(d)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if kind, _, ok := decodeHeader(raw); !ok || kind != hashKindCustom {
		t.Fatalf("header kind = %d (ok %v), want the caller-supplied kind", kind, ok)
	}

	reopened, err := NewFilter(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Contains(d) {
		t.Fatal("a filter reopened with the same index hash lost its bits")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	def, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer def.Close()
	if def.Contains(d) {
		t.Fatal("bits indexed by a caller-supplied hash were trusted by the default hash")
	}
	if kind, _, ok := decodeHeader(def.raw); !ok || kind != hashKindDefault {
		t.Fatalf("rebuilt header kind = %d (ok %v), want the default kind", kind, ok)
	}
}

// TestHeaderRoundTrip pins the fixed header layout and the checksum over the
// header's kind and key: the scheme and checksum bytes are written
// deterministically, and a header whose checked bytes changed is unusable rather
// than indexed under the changed key (go-cask#361).
func TestHeaderRoundTrip(t *testing.T) {
	key := indexKey{0x01, 0x02, 0x03}
	buf := make([]byte, headerSize)
	encodeHeader(buf, hashKindCustom, key)
	if !bytes.HasPrefix(buf, magic[:]) {
		t.Fatalf("header does not start with the magic: % x", buf[:8])
	}
	if buf[8] != hashKindCustom {
		t.Fatalf("kind byte = %d, want %d", buf[8], hashKindCustom)
	}
	if buf[9] != checksumSchemeCRC64 {
		t.Fatalf("checksum scheme byte = %d, want %d", buf[9], checksumSchemeCRC64)
	}
	want := headerChecksum(hashKindCustom, key)
	if got := buf[10:16]; !bytes.Equal(got, want[:]) {
		t.Fatalf("checksum bytes = % x, want % x", got, want)
	}
	kind, gotKey, ok := decodeHeader(buf)
	if !ok || kind != hashKindCustom || gotKey != key {
		t.Fatalf("decodeHeader = (kind %d, key %v, ok %v), want the encoded header", kind, gotKey, ok)
	}
	// A reader MUST NOT accept changed checked bytes — a key byte, the kind or
	// the checksum itself — because the key is the index.
	mutated := bytes.Clone(buf)
	mutated[20] ^= 0x01 // a byte of the 32-byte key
	if _, _, ok := decodeHeader(mutated); ok {
		t.Fatal("decodeHeader accepted a header whose key byte changed")
	}
	mutated = bytes.Clone(buf)
	mutated[10] ^= 0x80 // a checksum byte
	if _, _, ok := decodeHeader(mutated); ok {
		t.Fatal("decodeHeader accepted a header whose checksum byte changed")
	}
	mutated = bytes.Clone(buf)
	mutated[8] = hashKindDefault // the other kind this build knows
	if _, _, ok := decodeHeader(mutated); ok {
		t.Fatal("decodeHeader accepted a kind byte that changed under its checksum")
	}
	// A zero scheme is a file written before the checksum existed; any other
	// unknown value is a scheme this build has no rule for. Both are unusable.
	for _, scheme := range []byte{0, 0x7f} {
		mutated = bytes.Clone(buf)
		mutated[9] = scheme
		if _, _, ok := decodeHeader(mutated); ok {
			t.Fatalf("decodeHeader accepted checksum scheme %d", scheme)
		}
	}
}

// TestDecodeHeaderRejectsUnusableFiles covers every header a filter cannot index:
// too short, no magic, a kind this build does not know, and a header written
// before the checksum existed (its reserved bytes are zeros).
func TestDecodeHeaderRejectsUnusableFiles(t *testing.T) {
	short := make([]byte, headerSize-1)
	copy(short, magic[:])
	cases := []struct {
		name string
		src  []byte
	}{
		{"empty", nil},
		{"short", short},
		{"no magic", bytes.Repeat([]byte{0xff}, headerSize)},
		{"unknown kind", func() []byte {
			buf := make([]byte, headerSize)
			encodeHeader(buf, 9, indexKey{})
			return buf
		}()},
		{"pre-checksum header", func() []byte {
			// Exactly what this package wrote before go-cask#361: magic, kind and
			// key present, the seven reserved bytes zero.
			buf := make([]byte, headerSize)
			copy(buf[0:8], magic[:])
			buf[8] = hashKindDefault
			key := indexKey{0x01}
			copy(buf[16:headerSize], key[:])
			return buf
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := decodeHeader(tc.src); ok {
				t.Fatal("decodeHeader accepted a file this build cannot index")
			}
		})
	}
}

// TestHeaderMutationSweepNeverReportsARecordedDigestAbsent is the regression
// guard for go-cask#361. It flips one byte at a time across every byte of a
// persisted header — magic, kind, checksum and key — reopens the file, and
// requires the reopened filter either to still report the digest it recorded or
// to say that it rebuilt the bitset. The third outcome is the bug: Contains
// false with Rebuilt false, a changed index key quietly reindexing the bitset so
// a stored digest becomes an authoritative absence.
func TestHeaderMutationSweepNeverReportsARecordedDigestAbsent(t *testing.T) {
	d := cas.NewDigest([]byte("recorded before the header was mutated"))
	dir := t.TempDir()
	for off := range headerSize {
		for _, flip := range []byte{0x01, 0x80, 0xff} {
			path := filepath.Join(dir, fmt.Sprintf("mutated-%02d-%02x.bin", off, flip))
			f, err := New(path, 64, 0.01)
			if err != nil {
				t.Fatal(err)
			}
			f.Add(d)
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) < headerSize {
				t.Fatalf("filter file is %d bytes, want at least the %d-byte header", len(raw), headerSize)
			}
			raw[off] ^= flip
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}

			reopened, err := New(path, 64, 0.01)
			if err != nil {
				t.Fatalf("reopen after flipping header byte %d (^%#x): %v", off, flip, err)
			}
			lost := !reopened.Contains(d) && !reopened.Rebuilt()
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			if lost {
				t.Fatalf("header byte %d flipped with %#x silently lost a recorded digest: Contains = false and Rebuilt() = false", off, flip)
			}
		}
	}
}

// TestFilterRebuiltReportsTheBitsetProvenance pins Rebuilt as the caller's
// signal that a filter's negatives do not vouch for the store: a file with no
// usable header (a brand-new one included) reports a rebuild, an intact header
// does not, and a nil *Filter is not a panic. The flag survives Close, so a
// caller can read it after the filter is flushed.
func TestFilterRebuiltReportsTheBitsetProvenance(t *testing.T) {
	var nilFilter *Filter
	if nilFilter.Rebuilt() {
		t.Fatal("a nil *Filter reported a rebuild")
	}

	path := filepath.Join(t.TempDir(), "provenance.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Rebuilt() {
		t.Fatal("a filter opened over a brand-new file did not report that it started empty")
	}
	d := cas.NewDigest([]byte("provenance"))
	f.Add(d)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !f.Rebuilt() {
		t.Fatal("a closed filter stopped reporting the rebuild it performed")
	}

	reopened, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Rebuilt() {
		t.Fatal("a filter reopened over an intact header reported a rebuild")
	}
	if !reopened.Contains(d) {
		t.Fatal("a filter reopened over an intact header lost a recorded digest")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened.Rebuilt() {
		t.Fatal("an intact filter reported a rebuild only after Close")
	}
}

// TestFilterPersistentRebuildsAPreChecksumHeader covers the file this package
// wrote before go-cask#361: magic and kind are present, the key is there, but
// the seven reserved bytes are zeros, so nothing vouches for the key. The bitset
// is rebuilt rather than indexed under a key no checksum attests to — a lost
// hint, never a false absence — and the filter's own bits are the authority.
func TestFilterPersistentRebuildsAPreChecksumHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-checksum.bin")
	raw := make([]byte, headerSize+16)
	copy(raw[0:8], magic[:])
	raw[8] = hashKindDefault
	key := indexKey{0xAA, 0xBB}
	copy(raw[16:headerSize], key[:])
	for i := headerSize; i < len(raw); i++ {
		raw[i] = 0xff // the old file claimed every digest was present
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := New(path, 64, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Rebuilt() {
		t.Fatal("a pre-checksum header was trusted without a checksum")
	}
	if f.Contains(cas.NewDigest([]byte("anything"))) {
		t.Fatal("the pre-checksum file's all-ones bitset was indexed instead of rebuilt")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kind, key, ok := decodeHeader(written)
	if !ok || kind != hashKindDefault {
		t.Fatalf("rebuilt header = (kind %d, ok %v), want a usable default-kind header", kind, ok)
	}
	if key == (indexKey{}) {
		t.Fatal("the rebuilt header carries no fresh index key")
	}
}

// TestKeyedIndexHashIsDeterministic pins the property the whole fix rests on:
// the same key and input always give the same positions, and the probes of one
// digest do not collapse onto each other.
func TestKeyedIndexHashIsDeterministic(t *testing.T) {
	key := indexKey{0xAB}
	first, second := keyedIndexHash(key), keyedIndexHash(key)
	data := []byte("a digest")
	positions := map[uint64]bool{}
	for i := range 8 {
		got := first(data, i)
		if got != second(data, i) {
			t.Fatalf("index %d differs between two hashers built from the same key: %d vs %d", i, got, second(data, i))
		}
		positions[got%1024] = true
	}
	if len(positions) < 6 {
		t.Fatalf("the 8 probes of one digest collapsed onto %d distinct positions", len(positions))
	}
	other := keyedIndexHash(indexKey{0xCD})
	if first(data, 0) == other(data, 0) {
		t.Fatal("two different keys produced the same first position")
	}
}
