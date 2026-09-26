// Package bench owns the benchmark helper decisions: how a capture is named, and
// which capture a fresh run is compared against.
//
// The rule these functions exist for is ownership. A reference dump is a comparison
// point, so a capture that only compares must never write it, a deliberate refresh
// must archive the previous reference before replacing it, and a capture must never be
// written over an earlier one. The paths and the naming convention are the caller's;
// the decisions are not.
package bench

import (
	"path"
	"strconv"
	"time"
)

// StampLayout is the UTC stamp an archived capture carries: `20060102-150405`. The
// shape sorts chronologically as text, which is what lets a directory listing be read
// as a history.
const StampLayout = "20060102-150405"

// Stamp renders one capture's UTC stamp.
func Stamp(t time.Time) string {
	return t.UTC().Format(StampLayout)
}

// UniqueName returns a path in dir that no one has taken: `<base><ext>`, then
// `<base>-1<ext>`, `<base>-2<ext>`, and so on. taken is the caller's question, so the
// rule needs no filesystem — and a caller that archives a reference can never overwrite
// an earlier archive, which is what makes the archive a history instead of one slot.
func UniqueName(dir, base, ext string, taken func(string) bool) string {
	candidate := path.Join(dir, base+ext)
	for n := 1; taken(candidate); n++ {
		candidate = path.Join(dir, base+"-"+strconv.Itoa(n)+ext)
	}
	return candidate
}

// Capture is one benchmark capture that exists: where it is and when it was written.
type Capture struct {
	// Path is the capture's path, as the caller listed it.
	Path string
	// ModTime is when it was written: the archive's fallback order is chronological.
	ModTime time.Time
}

// Baseline returns the capture a fresh run is compared against: the one the caller
// named, else the canonical dump when it exists, else the newest archived capture. It
// reports false when there is nothing to compare against, which is the caller's error
// to report rather than an empty comparison.
//
// An explicit name is returned as given, even when it does not exist: a name the
// caller typed must not be silently replaced by something else, and the caller checks
// it and reports what is wrong with the name it was given.
func Baseline(explicit, canonical string, archived []Capture, exists func(string) bool) (string, bool) {
	if explicit != "" {
		return explicit, true
	}
	if canonical != "" && exists(canonical) {
		return canonical, true
	}
	newest := Newest(archived)
	return newest, newest != ""
}

// Newest returns the most recently written capture, or "" when there is none. Two
// captures written in the same instant are ordered by path, so the choice does not
// depend on the order the caller listed the directory in.
func Newest(captures []Capture) string {
	newest := ""
	var newestTime time.Time
	for _, capture := range captures {
		switch {
		case newest == "":
		case capture.ModTime.After(newestTime):
		case capture.ModTime.Equal(newestTime) && capture.Path < newest:
		default:
			continue
		}
		newest, newestTime = capture.Path, capture.ModTime
	}
	return newest
}
