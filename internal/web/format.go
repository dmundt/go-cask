// Presentation helpers. These turn stored values into the strings the templates
// render and decide nothing about what is stored.

package web

import (
	"fmt"
	"math"
	"runtime/debug"
	"strings"
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
// the package documents as this viewer's short form — so the marker is only
// added when Prefix actually dropped something.
func shortDigest(d cas.Digest) string {
	const shortChars = 8
	full := d.String()
	if len(full) <= shortChars {
		return full
	}
	return d.Prefix(shortChars) + "…"
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

// hexdump renders a classic 16-byte-row dump (offset, hex, ASCII).
func hexdump(data []byte) []dumpRow {
	var rows []dumpRow
	for off := 0; off < len(data); off += 16 {
		end := min(off+16, len(data))
		var hexParts []string
		var ascii strings.Builder
		for _, b := range data[off:end] {
			hexParts = append(hexParts, fmt.Sprintf("%02x", b))
			if b >= 32 && b < 127 {
				ascii.WriteByte(b)
			} else {
				ascii.WriteByte('.')
			}
		}
		rows = append(rows, dumpRow{fmt.Sprintf("%08x", off), strings.Join(hexParts, " "), ascii.String()})
	}
	return rows
}

type dumpRow struct {
	// Offset is the hexadecimal byte offset.
	Offset string
	// Hex is the formatted byte sequence.
	Hex string
	// ASCII is the printable representation.
	ASCII string
}
