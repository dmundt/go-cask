package bench

import (
	"testing"
	"time"
)

func TestStamp(t *testing.T) {
	t.Parallel()

	// A local time is stamped in UTC: two machines in different zones must name one
	// capture the same way, and the archive's names must sort chronologically.
	when := time.Date(2026, 9, 25, 14, 46, 49, 0, time.FixedZone("UTC+2", 2*60*60))
	if got, want := Stamp(when), "20260925-124649"; got != want {
		t.Errorf("Stamp = %q, want %q", got, want)
	}
}

func TestUniqueName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		taken map[string]bool
		want  string
	}{
		{name: "a free name is the base", want: "archive/baseline-20260925-124649.txt"},
		{
			name:  "a taken name moves by one",
			taken: map[string]bool{"archive/baseline-20260925-124649.txt": true},
			want:  "archive/baseline-20260925-124649-1.txt",
		},
		{
			name: "several taken names keep counting",
			taken: map[string]bool{
				"archive/baseline-20260925-124649.txt":   true,
				"archive/baseline-20260925-124649-1.txt": true,
				"archive/baseline-20260925-124649-2.txt": true,
			},
			want: "archive/baseline-20260925-124649-3.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UniqueName("archive", "baseline-20260925-124649", ".txt", func(path string) bool {
				return tc.taken[path]
			})
			if got != tc.want {
				t.Errorf("UniqueName = %q, want %q", got, tc.want)
			}
		})
	}
}

// captures builds an archive listing written in the given order, so the tests read as
// times rather than as timestamps.
func captures(paths ...string) []Capture {
	var listed []Capture
	for _, path := range paths {
		day := int(path[len(path)-5] - '0')
		listed = append(listed, Capture{Path: path, ModTime: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC)})
	}
	return listed
}

func TestNewest(t *testing.T) {
	t.Parallel()

	if got := Newest(nil); got != "" {
		t.Errorf("Newest(nil) = %q, want no capture", got)
	}
	// A name ending in the day it was written keeps the listing order unrelated to
	// the times, which is the point: the newest entry wins wherever it appears.
	if got := Newest(captures("day1.txt", "day3.txt", "day2.txt")); got != "day3.txt" {
		t.Errorf("Newest = %q, want the most recently written capture", got)
	}
	// Two captures in the same instant are ordered by path, so the choice is stable.
	same := []Capture{
		{Path: "b.txt", ModTime: time.Unix(0, 0)},
		{Path: "a.txt", ModTime: time.Unix(0, 0)},
	}
	if got := Newest(same); got != "a.txt" {
		t.Errorf("Newest = %q, want the path that sorts first on a tie", got)
	}
}

func TestBaseline(t *testing.T) {
	t.Parallel()

	archived := []Capture{
		{Path: "archive/baseline-20260101-000000.txt", ModTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "archive/baseline-20260201-000000.txt", ModTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
	}
	exists := func(path string) bool { return path == "data/baseline.txt" }

	cases := []struct {
		name      string
		explicit  string
		canonical string
		exists    func(string) bool
		want      string
		found     bool
	}{
		{
			name:     "an explicit capture is used as given",
			explicit: "some/other.txt",
			want:     "some/other.txt",
			found:    true,
		},
		{
			name:      "the canonical dump when it exists",
			canonical: "data/baseline.txt",
			exists:    exists,
			want:      "data/baseline.txt",
			found:     true,
		},
		{
			name:      "the newest archive when the canonical dump is gone",
			canonical: "data/baseline.txt",
			exists:    func(string) bool { return false },
			want:      "archive/baseline-20260201-000000.txt",
			found:     true,
		},
		{
			name:      "nothing to compare against",
			canonical: "data/baseline.txt",
			exists:    func(string) bool { return false },
		},
		{
			// A name the caller typed is never replaced by a fallback; the caller
			// reports that it does not exist.
			name:     "an explicit capture is not replaced by the archive",
			explicit: "missing.txt",
			want:     "missing.txt",
			found:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			listed := archived
			if tc.name == "nothing to compare against" {
				listed = nil
			}
			if tc.exists == nil {
				tc.exists = func(string) bool { return false }
			}
			got, found := Baseline(tc.explicit, tc.canonical, listed, tc.exists)
			if found != tc.found {
				t.Fatalf("Baseline found = %v, want %v", found, tc.found)
			}
			if got != tc.want {
				t.Errorf("Baseline = %q, want %q", got, tc.want)
			}
		})
	}
}
