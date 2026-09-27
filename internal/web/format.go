// Presentation helpers. These turn stored values into the strings the templates
// render and decide nothing about what is stored.

package web

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"runtime/debug"
	"time"

	"github.com/dmundt/go-cask/cas"
)

func formatWritten(written time.Time) string {
	return formatWrittenAt(written, time.Now())
}

func formatWrittenAt(written, now time.Time) string {
	if written.IsZero() {
		return ""
	}
	age := max(now.Sub(written), 0)
	minutes := int(age / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm ago", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh ago", hours)
	}
	return fmt.Sprintf("%dd ago", hours/24)
}

// formatChecked renders a verification timestamp. A verification result is
// only as good as its age, so the label states the clock time and the elapsed
// age together.
func formatChecked(checked time.Time) string {
	if checked.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s)", checked.UTC().Format("2006-01-02 15:04:05 UTC"), formatWritten(checked))
}

// checkedLabel renders when an object was last verified in this session.
func checkedLabel(store *sessions, id, digest string) string {
	_, checked := store.verificationRecord(id, digest)
	return formatChecked(checked)
}

// storedReport replays the last check of digest in this session, or nil when
// there is none. The check time is stamped on at render time because the label
// carries a relative age that keeps moving after the check ran.
func storedReport(store *sessions, id, digest string) *actionOutcome {
	_, checked, report := store.verificationReport(id, digest)
	if checked.IsZero() {
		return nil
	}
	report.Checked = formatChecked(checked)
	return &report
}

func formatTimestamp(written time.Time) string {
	if written.IsZero() {
		return ""
	}
	return written.UTC().Format(time.RFC3339Nano)
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 10 || value == math.Trunc(value) {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

// shortDigest renders a digest in the viewer's abbreviated form: the first 8
// hex characters plus an ellipsis, or the whole digest when it is no longer
// than that. The 8-character short form is cas.Digest.Prefix(8) — the helper
// the package documents as this viewer's short form — taken here from the hex
// form already rendered rather than encoded a second time inside Prefix: a
// rendered object row therefore costs two hex renderings, not four
// (performance.md §4). The marker is only added when the short form actually
// dropped something.
func shortDigest(d cas.Digest) string {
	const shortChars = 8
	full := d.String()
	if len(full) <= shortChars {
		return full
	}
	return full[:shortChars] + "…"
}

// Version reports the build's module version, rendered as the viewer and the
// CLI both show it. It comes from build info, so it is a pseudo-version until
// the first tag and "dev" for an untracked build (versioning §2).
func Version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return "dev"
}

// hexdump renders a classic 16-byte-row dump (offset, hex, ASCII). The Bytes
// tab renders one of these per inspector reveal over a preview of at most
// previewLimit bytes, so each row is encoded in a single encoding/hex pass into
// a reused stack buffer instead of formatting every byte through fmt
// (performance.md §4: avoid fmt in hot paths, use encoding/hex directly, not
// %x loops). The rendered columns are unchanged: an eight-digit lowercase
// offset, space-separated lowercase byte pairs, and a printable-ASCII column
// whose unreadable bytes are '.'.
func hexdump(data []byte) []dumpRow {
	rows := make([]dumpRow, 0, (len(data)+15)/16)
	var encoded [16 * 2]byte
	var spaced [16*3 - 1]byte
	for off := 0; off < len(data); off += 16 {
		row := data[off:min(off+16, len(data))]

		hex.Encode(encoded[:], row)
		for i := range row {
			if i > 0 {
				spaced[i*3-1] = ' '
			}
			spaced[i*3] = encoded[i*2]
			spaced[i*3+1] = encoded[i*2+1]
		}

		var ascii [16]byte
		for i, b := range row {
			if b >= 32 && b < 127 {
				ascii[i] = b
			} else {
				ascii[i] = '.'
			}
		}

		rows = append(rows, dumpRow{
			Offset: hexOffset(off),
			Hex:    string(spaced[:3*len(row)-1]),
			ASCII:  string(ascii[:len(row)]),
		})
	}
	return rows
}

// hexOffset renders a byte offset as the dump's eight-digit lowercase hex
// column — the column fmt's %08x produced. A 32-bit byte position holds every
// offset the bounded preview can reach (viewer-design §3).
func hexOffset(off int) string {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], uint32(off))
	var out [8]byte
	hex.Encode(out[:], raw[:])
	return string(out[:])
}

type dumpRow struct {
	// Offset is the hexadecimal byte offset.
	Offset string
	// Hex is the formatted byte sequence.
	Hex string
	// ASCII is the printable representation.
	ASCII string
}
