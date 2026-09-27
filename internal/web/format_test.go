// Tests for the presentation helpers in format.go.

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
)

func TestFormatWrittenAt(t *testing.T) {
	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		written time.Time
		want    string
	}{
		{name: "missing", want: ""},
		{name: "minutes", written: now.Add(-59 * time.Minute), want: "59m ago"},
		{name: "hours", written: now.Add(-23 * time.Hour), want: "23h ago"},
		{name: "day", written: now.Add(-24 * time.Hour), want: "1d ago"},
		{name: "days", written: now.Add(-72 * time.Hour), want: "3d ago"},
		{name: "future", written: now.Add(time.Hour), want: "0m ago"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatWrittenAt(test.written, now); got != test.want {
				t.Errorf("formatWrittenAt() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	timestamp := time.Date(2026, time.September, 21, 12, 10, 48, 123456789, time.FixedZone("UTC+2", 2*60*60))
	if got, want := formatTimestamp(timestamp), "2026-09-21T10:10:48.123456789Z"; got != want {
		t.Errorf("formatTimestamp() = %q, want %q", got, want)
	}
	if got := formatTimestamp(time.Time{}); got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1 KiB"},
		{1536, "1.5 KiB"},
		{10 * 1024, "10 KiB"},
		{1024 * 1024, "1 MiB"},
		{128 << 20, "128 MiB"},
		{1 << 30, "1 GiB"},
	}
	for _, test := range tests {
		if got := formatBytes(test.size); got != test.want {
			t.Errorf("formatBytes(%d) = %q, want %q", test.size, got, test.want)
		}
	}
}

// TestShortDigest pins the abbreviated form the object browser lists
// (viewer-design §3): the first eight hex characters plus the ellipsis marker,
// or the whole form when it is no longer than that.
func TestShortDigest(t *testing.T) {
	digest, err := cas.ParseDigest(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		digest cas.Digest
		want   string
	}{
		{name: "absent", want: ""},
		{name: "shorter than the short form", digest: cas.NewDigest([]byte{0xab, 0xcd}), want: "abcd"},
		{name: "exactly the short form", digest: cas.NewDigest([]byte{1, 2, 3, 4}), want: "01020304"},
		{name: "longer than the short form", digest: cas.NewDigest([]byte{1, 2, 3, 4, 5}), want: "01020304…"},
		{name: "a full sha256 digest", digest: digest, want: "abababab…"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shortDigest(test.digest); got != test.want {
				t.Errorf("shortDigest(%d bytes) = %q, want %q", len(test.digest), got, test.want)
			}
		})
	}
}

// TestShortDigestAgreesWithPrefix sweeps every digest width the viewer can
// receive and requires the abbreviated form to stay exactly what
// cas.Digest.Prefix(8) plus the marker produces — the contract the helper's
// comment states, and the thing a second hex encoding used to supply.
func TestShortDigestAgreesWithPrefix(t *testing.T) {
	for width := range 65 {
		digest := cas.NewDigest(make([]byte, width))
		want := digest.Prefix(8)
		if full := digest.String(); len(full) > 8 {
			want += "…"
		}
		if got := shortDigest(digest); got != want {
			t.Fatalf("shortDigest(%d bytes) = %q, want %q", width, got, want)
		}
	}
}

// TestHexdumpKeepsTheDumpColumns pins the Bytes tab's dump format, which the
// encoding/hex rewrite must reproduce byte for byte: an eight-digit lowercase
// offset, lowercase space-separated byte pairs, and printable ASCII with the
// not-readable marker (viewer-design §3).
func TestHexdumpKeepsTheDumpColumns(t *testing.T) {
	payload := append([]byte("CASK viewer bytes tab: 16-byte rows."), 0x00, 0x1f, 0x20, 0x7e, 0x7f)
	want := []dumpRow{
		{Offset: "00000000", Hex: "43 41 53 4b 20 76 69 65 77 65 72 20 62 79 74 65", ASCII: "CASK viewer byte"},
		{Offset: "00000010", Hex: "73 20 74 61 62 3a 20 31 36 2d 62 79 74 65 20 72", ASCII: "s tab: 16-byte r"},
		{Offset: "00000020", Hex: "6f 77 73 2e 00 1f 20 7e 7f", ASCII: "ows... ~."},
	}
	got := hexdump(payload)
	if len(got) != len(want) {
		t.Fatalf("hexdump(%d bytes) = %d rows, want %d", len(payload), len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if rows := hexdump(nil); len(rows) != 0 {
		t.Errorf("hexdump(nil) = %d rows, want 0", len(rows))
	}
}

// TestHexdumpViewsTheWholePreview pins the shape of the largest dump a request
// renders: previewLimit bytes as full rows whose columns are exactly the widths
// the template's field padding expects.
func TestHexdumpViewsTheWholePreview(t *testing.T) {
	rows := hexdump(benchmarkDumpPayload())
	if len(rows) != previewLimit/16 {
		t.Fatalf("hexdump(%d bytes) = %d rows, want %d", previewLimit, len(rows), previewLimit/16)
	}
	for i, row := range rows {
		if want := fmt.Sprintf("%08x", i*16); row.Offset != want {
			t.Errorf("row %d offset = %q, want %q", i, row.Offset, want)
		}
		if want := 16*3 - 1; len(row.Hex) != want {
			t.Errorf("row %d hex column = %d characters, want %d", i, len(row.Hex), want)
		}
		if len(row.ASCII) != 16 {
			t.Errorf("row %d ASCII column = %d characters, want 16", i, len(row.ASCII))
		}
	}
	// The printable window's edges: 0x20 and 0x7e are shown, 0x1f and 0x7f are
	// not.
	if got, want := rows[2].ASCII, ` !"#$%&'()*+,-./`; got != want {
		t.Errorf("row 2 ASCII = %q, want %q", got, want)
	}
	if got, want := rows[7].Hex, "70 71 72 73 74 75 76 77 78 79 7a 7b 7c 7d 7e 7f"; got != want {
		t.Errorf("row 7 hex = %q, want %q", got, want)
	}
	if got := rows[7].ASCII; !strings.HasSuffix(got, "~.") {
		t.Errorf("row 7 ASCII = %q, want the 0x7e byte shown and 0x7f marked", got)
	}
}

// TestHexOffsetMatchesFmt pins the offset column against the %08x it replaced:
// lowercase, zero-padded to eight digits, and identical for every offset the
// preview can reach.
func TestHexOffsetMatchesFmt(t *testing.T) {
	for _, off := range []int{0, 15, 16, 240, 4096, 1 << 24, 1<<31 - 1} {
		if got, want := hexOffset(off), fmt.Sprintf("%08x", off); got != want {
			t.Errorf("hexOffset(%d) = %q, want %q", off, got, want)
		}
	}
}
