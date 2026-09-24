package persistent

import (
	"bytes"
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

// TestHeaderRoundTrip pins the fixed header layout and its tolerance of the
// reserved bytes.
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
	if got := buf[9:16]; !bytes.Equal(got, make([]byte, 7)) {
		t.Fatalf("reserved bytes = % x, want zeros", got)
	}
	kind, gotKey, ok := decodeHeader(buf)
	if !ok || kind != hashKindCustom || gotKey != key {
		t.Fatalf("decodeHeader = (kind %d, key %v, ok %v), want the encoded header", kind, gotKey, ok)
	}
	// A reader MUST ignore the reserved bytes rather than reject them.
	buf[9] = 0x7f
	if _, _, ok := decodeHeader(buf); !ok {
		t.Fatal("decodeHeader rejected a header with a used reserved byte")
	}
}

// TestDecodeHeaderRejectsUnusableFiles covers every header a filter cannot index:
// too short, no magic, and a kind this build does not know.
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := decodeHeader(tc.src); ok {
				t.Fatal("decodeHeader accepted a file this build cannot index")
			}
		})
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
