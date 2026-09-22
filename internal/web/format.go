// Presentation helpers. These turn stored values into the strings the templates
// render and decide nothing about what is stored.

package web

import (
	"encoding/hex"
	"fmt"
	"html/template"
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
	age := now.Sub(written)
	if age < 0 {
		age = 0
	}
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

func shortDigest(d cas.Digest) string {
	const prefixChars = 8
	if len(d)*2 <= prefixChars {
		return d.String()
	}
	var short [prefixChars + len("…")]byte
	hex.Encode(short[:prefixChars], d[:prefixChars/2])
	copy(short[prefixChars:], "…")
	return string(short[:])
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

// selectionLinkAttrs renders the attributes every link that selects an object
// carries: the href for a cold click, the htmx request that swaps the inspector
// in place, and the header that marks the request as a selection. Row cells,
// inspector tabs, history arrows, and reference links all navigate the same
// way, so they all emit these from here — a copy that quietly lost the
// selection header would still look right. The URL is escaped because the
// result is injected as raw attribute text.
func selectionLinkAttrs(selectURL string) template.HTMLAttr {
	escaped := template.HTMLEscapeString(selectURL)
	return template.HTMLAttr(fmt.Sprintf(
		`href="%s" hx-get="%s" hx-target="#object-inspector" hx-headers='{"X-Viewer-Selection":"true"}' hx-push-url="true"`,
		escaped, escaped))
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
