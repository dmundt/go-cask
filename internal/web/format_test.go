// Tests for the presentation helpers in format.go.

package web

import (
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

func TestShortDigest(t *testing.T) {
	digest, err := cas.ParseDigest(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if got := shortDigest(digest); got != "abababab…" {
		t.Errorf("shortDigest() = %q, want %q", got, "abababab…")
	}
}
