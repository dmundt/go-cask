package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/internal/index"
)

// censusFixture seeds the store every census test reads: the deterministic
// preview graph (current-format frames carrying the `preview` codec tag) plus
// one raw object written by `put`, which has no envelope at all.
func censusFixture(t *testing.T) modeFlags {
	t.Helper()
	mf := localMF(t)
	if _, code := run(t, mf, "seed-preview", "-count", "8"); code != 0 {
		t.Fatalf("seed-preview exit = %d, want 0", code)
	}
	path := filepath.Join(t.TempDir(), "raw.bin")
	if err := os.WriteFile(path, []byte("not an envelope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, mf, "put", path); code != 0 {
		t.Fatalf("put exit = %d, want 0", code)
	}
	return mf
}

// listJSON is the shape `cask list -json` returns.
type listJSON struct {
	Total   int `json:"total"`
	Objects []struct {
		Hash    string `json:"hash"`
		Type    string `json:"type"`
		Version byte   `json:"version"`
		Codec   string `json:"codec"`
		Size    int64  `json:"size"`
	} `json:"objects"`
}

func decodeListJSON(t *testing.T, out string) listJSON {
	t.Helper()
	var decoded listJSON
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("list -json is not JSON: %v\n%.300q", err, out)
	}
	return decoded
}

// metaJSON is the shape `cask meta -json` returns.
type metaJSON struct {
	Hash    string `json:"hash"`
	Type    string `json:"type"`
	Version byte   `json:"version"`
	Codec   string `json:"codec"`
}

func decodeMetaJSON(t *testing.T, out string) metaJSON {
	t.Helper()
	var decoded metaJSON
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("meta -json is not JSON: %v\n%.300q", err, out)
	}
	return decoded
}

// TestListAndMetaAgreeOnTheHeader pins the cross-surface contract: for every
// digest in one store, `cask list -json` and `cask meta -json` report the same
// type, frame version and codec — the preview frames as 2/preview, the raw
// object written by `put` as no header at all (type "", version 0) with an
// explicitly unspecified codec.
func TestListAndMetaAgreeOnTheHeader(t *testing.T) {
	mf := censusFixture(t)
	listed := decodeListJSON(t, mustRun(t, mf, "list", "-json"))
	if listed.Total != len(listed.Objects) {
		t.Fatalf("list total = %d, want %d listed objects", listed.Total, len(listed.Objects))
	}
	if len(listed.Objects) == 0 {
		t.Fatal("the fixture store lists no objects")
	}
	sawFrame, sawRaw := false, false
	for _, object := range listed.Objects {
		meta := decodeMetaJSON(t, mustRun(t, mf, "meta", "-json", object.Hash))
		if meta.Type != object.Type || meta.Version != object.Version || meta.Codec != object.Codec {
			t.Fatalf("meta %s = {type %q, version %d, codec %q}, list says {type %q, version %d, codec %q}",
				object.Hash, meta.Type, meta.Version, meta.Codec, object.Type, object.Version, object.Codec)
		}
		switch object.Version {
		case 2:
			sawFrame = true
			if object.Codec != "preview" {
				t.Fatalf("seeded %s codec = %q, want preview", object.Hash, object.Codec)
			}
		case 0:
			sawRaw = true
			if object.Type != "" || object.Codec != index.UnspecifiedCodec {
				t.Fatalf("raw %s = {type %q, codec %q}, want no header and the census's unspecified codec %q",
					object.Hash, object.Type, object.Codec, index.UnspecifiedCodec)
			}
		default:
			t.Fatalf("%s reports frame version %d, want 2 (seeded) or 0 (raw)", object.Hash, object.Version)
		}
	}
	if !sawFrame || !sawRaw {
		t.Fatalf("fixture must hold both a framed and a raw object (framed=%v raw=%v)", sawFrame, sawRaw)
	}
}

// TestListFiltersOnTheHeader pins the filters: each lists exactly the digests
// whose `meta` reports the value, a bare type name means its first major
// version, and a value no object carries is an empty result at exit 0.
func TestListFiltersOnTheHeader(t *testing.T) {
	mf := censusFixture(t)
	all := decodeListJSON(t, mustRun(t, mf, "list", "-json"))

	wantPreview := map[string]bool{}
	wantRaw := map[string]bool{}
	wantBlob := map[string]bool{}
	for _, object := range all.Objects {
		if object.Codec == "preview" {
			wantPreview[object.Hash] = true
		} else {
			wantRaw[object.Hash] = true
		}
		if object.Type == "blob@1" {
			wantBlob[object.Hash] = true
		}
	}

	for _, tc := range []struct {
		name string
		args []string
		want map[string]bool
	}{
		{"codec tag", []string{"list", "-codec", "preview", "-json"}, wantPreview},
		{"unspecified codec", []string{"list", "-codec", "unspecified", "-json"}, wantRaw},
		{"versioned type", []string{"list", "-type", "blob@1", "-json"}, wantBlob},
		{"bare type means @1", []string{"list", "-type", "blob", "-json"}, wantBlob},
		{"unknown type", []string{"list", "-type", "nothing@1", "-json"}, map[string]bool{}},
		{"unknown codec", []string{"list", "-codec", "nope", "-json"}, map[string]bool{}},
	} {
		filtered := decodeListJSON(t, mustRun(t, mf, tc.args...))
		got := make(map[string]bool, len(filtered.Objects))
		for _, object := range filtered.Objects {
			got[object.Hash] = true
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s: %d objects, want %d", tc.name, len(got), len(tc.want))
		}
		for hash := range tc.want {
			if !got[hash] {
				t.Fatalf("%s: %s missing from the filtered list", tc.name, hash)
			}
		}
		if filtered.Total != len(tc.want) {
			t.Fatalf("%s: total = %d, want the %d matching objects", tc.name, filtered.Total, len(tc.want))
		}
	}
}

// statsJSON is the shape `cask stats -json` returns.
type statsJSON struct {
	Objects    int            `json:"objects"`
	Unreadable int            `json:"unreadable"`
	Headerless int            `json:"headerless"`
	Types      map[string]int `json:"types"`
	Versions   map[string]int `json:"versions"`
	Codecs     map[string]int `json:"codecs"`
}

// TestStatsCensusSumsToTheObjectCount pins the census arithmetic: every axis
// sums to the object count minus the objects that carry no header fields to
// count — in this fixture exactly the raw object `put` wrote — and the axes
// agree with what list and meta report.
func TestStatsCensusSumsToTheObjectCount(t *testing.T) {
	mf := censusFixture(t)
	var census statsJSON
	if err := json.Unmarshal([]byte(mustRun(t, mf, "stats", "-json")), &census); err != nil {
		t.Fatalf("stats -json is not JSON: %v", err)
	}
	if census.Objects == 0 {
		t.Fatal("the fixture store reports no objects")
	}
	if census.Unreadable != 0 {
		t.Fatalf("unreadable = %d, want 0 in this fixture", census.Unreadable)
	}
	if census.Headerless != 1 {
		t.Fatalf("headerless = %d, want exactly the raw object `put` wrote", census.Headerless)
	}
	for axis, counts := range map[string]map[string]int{
		"types": census.Types, "versions": census.Versions, "codecs": census.Codecs,
	} {
		sum := 0
		for _, count := range counts {
			sum += count
		}
		if want := census.Objects - census.Unreadable - census.Headerless; sum != want {
			t.Fatalf("%s sums to %d, want %d (objects %d - unreadable %d - headerless %d)",
				axis, sum, want, census.Objects, census.Unreadable, census.Headerless)
		}
	}
	if got := census.Codecs["preview"]; got == 0 {
		t.Fatalf("codecs = %v, want the seeded preview frames counted", census.Codecs)
	}
	if _, ok := census.Codecs["unspecified"]; ok {
		t.Fatalf("codecs = %v, must not count the headerless object as an unspecified codec", census.Codecs)
	}
	if got := census.Versions["2"]; got == 0 {
		t.Fatalf("versions = %v, want the seeded frames counted under 2", census.Versions)
	}
	if _, ok := census.Versions["0"]; ok {
		t.Fatalf("versions = %v, must not invent a version 0 for the headerless object", census.Versions)
	}
}

// mustRun runs a cask operation that is expected to succeed and returns stdout.
func mustRun(t *testing.T, mf modeFlags, args ...string) string {
	t.Helper()
	out, code := run(t, mf, args[0], args[1:]...)
	if code != 0 {
		t.Fatalf("%v exit = %d, want 0", args, code)
	}
	return out
}
