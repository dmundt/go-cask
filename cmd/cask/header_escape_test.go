package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// craftedCodec and craftedType frame an envelope whose codec tag and type name
// carry terminal control characters. cas.EncodeEnvelope is the same writer the
// store uses, so the bytes are a valid frame an attacker can hand `cask put`
// (go-cask#354) — nothing on the write path restricts the character set.
const (
	craftedCodec = "js\x1bon"       // ESC
	craftedType  = "bl\x1bot@\n1\r" // ESC, LF, CR
)

// storeCraftedHeaderFrame puts one crafted frame into the store and returns the
// digest `cask put` printed.
func storeCraftedHeaderFrame(t *testing.T, mf modeFlags) string {
	t.Helper()
	frame, err := cas.EncodeEnvelope(craftedCodec, craftedType, []byte("payload"))
	if err != nil {
		t.Fatalf("EncodeEnvelope(%q, %q) = %v, want a valid crafted frame", craftedCodec, craftedType, err)
	}
	path := filepath.Join(t.TempDir(), "crafted.bin")
	if err := os.WriteFile(path, frame, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, mf, "put", path)
	if code != 0 {
		t.Fatalf("put of the crafted frame exit = %d, want 0", code)
	}
	return strings.TrimSpace(out)
}

// TestSafeFieldStripsControlCharacters pins the rendering rule #354 records
// (cli.md §4): a string read from stored bytes loses its control characters and
// keeps every other byte, so an ordinary name is unchanged and stays
// copy-pasteable — the reason the rule is a sanitizer and not %q.
func TestSafeFieldStripsControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"ordinary type", "blob@1", "blob@1"},
		{"ordinary codec", "json", "json"},
		{"the unspecified label", "unspecified", "unspecified"},
		{"empty", "", ""},
		{"C0 controls", "a\x00\x07\x08\x09\x0a\x0d\x1bb", "a???????b"},
		{"DEL", "a\x7fb", "a?b"},
		{"C1 controls", "a\u0080\u009fb", "a??b"},
		{"a lone ESC", "\x1b", "?"},
		{"printable non-ASCII survives", "bl\u00f6b@1", "bl\u00f6b@1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeField(tc.in); got != tc.want {
				t.Fatalf("safeField(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStatsAndMetaSanitizeStoredHeaderStrings is #354's acceptance test: an
// object whose envelope type name and codec tag carry ESC, LF, CR and BEL is
// printed by `stats` and `meta` without any of them, the census keeps its
// one-line-per-axis shape — an embedded newline can neither forge a census line
// nor break the format — and the `-json` output is unchanged, because
// encoding/json is the escaping for the machine-readable path.
func TestStatsAndMetaSanitizeStoredHeaderStrings(t *testing.T) {
	mf := localMF(t)
	hash := storeCraftedHeaderFrame(t, mf)

	out, code := run(t, mf, "stats")
	if code != 0 {
		t.Fatalf("stats exit = %d, want 0", code)
	}
	for _, control := range []string{"\x1b", "\r", "\x07"} {
		if strings.Contains(out, control) {
			t.Fatalf("stats printed the raw control character %q: %q", control, out)
		}
	}
	if !strings.Contains(out, "types: bl?ot@?1?=1") {
		t.Fatalf("stats = %s, want the type axis rendered with control characters replaced", out)
	}
	if !strings.Contains(out, "codecs: js?on=1") {
		t.Fatalf("stats = %s, want the codec axis rendered with control characters replaced", out)
	}
	// The report's own shape: the summary line, then one line per axis whose
	// label is exactly the axis name. A newline embedded in a stored name
	// would produce a line that is neither, so this is what says the census
	// cannot be forged.
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasSuffix(lines[0], " bytes") {
		t.Fatalf("stats = %q, want the object/byte summary first", out)
	}
	for _, line := range lines[1:] {
		axis, _, found := strings.Cut(line, ":")
		if !found || (axis != "types" && axis != "versions" && axis != "codecs") {
			t.Fatalf("stats line %q is not a census axis, so a stored name forged a line", line)
		}
	}

	t.Run("meta", func(t *testing.T) {
		out, code := run(t, mf, "meta", hash)
		if code != 0 {
			t.Fatalf("meta exit = %d, want 0", code)
		}
		// The type keeps its %q rendering — that already escapes every control
		// byte and quotes a name an operator may need to delimit — while the
		// codec is sanitized by the shared helper. One line means no raw
		// control byte survives in either field.
		if lines := strings.Count(out, "\n"); lines != 1 {
			t.Fatalf("meta printed %d lines (%q), want one", lines, out)
		}
		for _, control := range []string{"\x1b", "\r", "\x07"} {
			if strings.Contains(out, control) {
				t.Fatalf("meta printed the raw control character %q: %q", control, out)
			}
		}
		if !strings.Contains(out, "type=\"bl\\x1bot@\\n1\\r\"") {
			t.Fatalf("meta = %q, want the type quoted with its control bytes escaped", out)
		}
		if !strings.Contains(out, "codec=js?on") {
			t.Fatalf("meta = %q, want the codec sanitized", out)
		}
	})

	t.Run("stats -json is unchanged", func(t *testing.T) {
		out, code := run(t, mf, "stats", "-json")
		if code != 0 {
			t.Fatalf("stats -json exit = %d, want 0", code)
		}
		var census struct {
			Types  map[string]int `json:"types"`
			Codecs map[string]int `json:"codecs"`
		}
		if err := json.Unmarshal([]byte(out), &census); err != nil {
			t.Fatalf("stats -json = %q: %v", out, err)
		}
		if census.Types[craftedType] != 1 {
			t.Fatalf("stats -json types = %q, want the stored type %q verbatim", census.Types, craftedType)
		}
		if census.Codecs[craftedCodec] != 1 {
			t.Fatalf("stats -json codecs = %q, want the stored codec %q verbatim", census.Codecs, craftedCodec)
		}
	})

	t.Run("meta -json is unchanged", func(t *testing.T) {
		out, code := run(t, mf, "meta", "-json", hash)
		if code != 0 {
			t.Fatalf("meta -json exit = %d, want 0", code)
		}
		var meta struct {
			Type  string `json:"type"`
			Codec string `json:"codec"`
		}
		if err := json.Unmarshal([]byte(out), &meta); err != nil {
			t.Fatalf("meta -json = %q: %v", out, err)
		}
		if meta.Type != craftedType || meta.Codec != craftedCodec {
			t.Fatalf("meta -json = {type %q, codec %q}, want the stored header %q/%q verbatim",
				meta.Type, meta.Codec, craftedType, craftedCodec)
		}
	})
}
