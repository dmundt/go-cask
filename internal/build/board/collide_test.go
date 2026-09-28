package board

import (
	"strings"
	"testing"
)

// points are the serialization points docs/specs/coordination.md §4 names, as go-cask passes
// them in.
var points = []string{
	"CHANGELOG.md",
	"docs/index.md",
	"docs/design/package-graph.md",
	"docs/specs/defaults.md",
	"docs/specs/cas-core.md",
	"docs/specs/landing.md",
	"internal/build/policy/policy.go",
}

// TestCollideSeparatesSerializationPoints pins the rule that decides a wave: two lanes in one
// serialization point conflict by construction, and that is reported apart from an ordinary
// file overlap, which may still merge cleanly.
func TestCollideSeparatesSerializationPoints(t *testing.T) {
	lanes := []Files{
		{Issue: "438", Name: "chore/438", Changed: Ranges{
			"CHANGELOG.md":                    {{30, 33}},
			"cmd/gate/main.go":                {{205, 210}},
			"internal/build/board/board.go":   {{1, 120}},
			"docs/index.md":                   {{42, 42}},
			"internal/build/policy/policy.go": {{120, 121}},
		}},
		{Issue: "440", Name: "chore/440", Changed: Ranges{
			"CHANGELOG.md":                  {{31, 32}},
			"docs/index.md":                 {{42, 43}},
			"internal/build/board/parse.go": {{1, 90}},
		}},
		{Issue: "441", Name: "chore/441", Changed: Ranges{
			"internal/build/board/parse.go": {{95, 120}},
			"docs/specs/defaults.md":        {{10, 11}},
		}},
	}

	report := Collide(lanes, points, []string{"the fetch failed"})
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "fetch failed") {
		t.Errorf("notes = %v, want the caller's caveat kept", report.Notes)
	}
	if !report.Conflicting() {
		t.Fatal("Conflicting = false, want true: three lanes share serialization points")
	}

	got := map[string]Serialized{}
	for _, point := range report.Points {
		got[point.File] = point
	}
	if len(report.Points) != len(points) {
		t.Fatalf("got %d serialization points, want every one of the %d named", len(report.Points), len(points))
	}
	if point := got["CHANGELOG.md"]; !point.Conflict || strings.Join(point.Lanes, ",") != "#438,#440" {
		t.Errorf("CHANGELOG.md = %+v, want a conflict between #438 and #440", point)
	}
	if point := got["docs/index.md"]; !point.Conflict || strings.Join(point.Lanes, ",") != "#438,#440" {
		t.Errorf("docs/index.md = %+v, want a conflict between #438 and #440", point)
	}
	if point := got["docs/specs/defaults.md"]; point.Conflict || strings.Join(point.Lanes, ",") != "#441" {
		t.Errorf("docs/specs/defaults.md = %+v, want one lane and no conflict", point)
	}
	if point := got["internal/build/policy/policy.go"]; point.Conflict || strings.Join(point.Lanes, ",") != "#438" {
		t.Errorf("policy.go = %+v, want one lane and no conflict", point)
	}
	if point := got["docs/specs/landing.md"]; point.Conflict || len(point.Lanes) != 0 {
		t.Errorf("landing.md = %+v, want an untouched point listed anyway", point)
	}

	// The serialization points are not repeated in the ordinary matrix: a reader who saw
	// them there would count one problem twice.
	for _, overlap := range report.Overlaps {
		if _, isPoint := got[overlap.File]; isPoint {
			t.Errorf("serialization point %s appeared as an ordinary overlap", overlap.File)
		}
	}
}

// TestCollideOrdinaryOverlaps pins the ordinary matrix: which two lanes touch one file, and
// whether their line spans actually collide. A shared file with disjoint spans may merge
// cleanly; a shared file with intersecting spans will not.
func TestCollideOrdinaryOverlaps(t *testing.T) {
	lanes := []Files{
		{Issue: "438", Name: "chore/438", Changed: Ranges{
			"internal/build/board/board.go": {{1, 60}},
			"cmd/gate/board.go":             {{1, 20}},
			"assets/logo.png":               nil,
		}},
		{Issue: "440", Name: "chore/440", Changed: Ranges{
			"internal/build/board/board.go": {{40, 120}},
			"cmd/gate/board.go":             {{30, 40}},
			"assets/logo.png":               nil,
		}},
	}

	report := Collide(lanes, points, nil)
	if report.Conflicting() {
		t.Error("Conflicting = true, want false: no serialization point is shared")
	}
	if len(report.Overlaps) != 3 {
		t.Fatalf("got %d overlaps, want 3: %+v", len(report.Overlaps), report.Overlaps)
	}
	byFile := map[string]Overlap{}
	for _, overlap := range report.Overlaps {
		byFile[overlap.File] = overlap
	}

	colliding := byFile["internal/build/board/board.go"]
	if colliding.A != "chore/438" || colliding.B != "chore/440" {
		t.Errorf("board.go pair = %q/%q, want the two lanes in order", colliding.A, colliding.B)
	}
	if colliding.IssueA != "438" || colliding.IssueB != "440" {
		t.Errorf("board.go issues = %q/%q, want both", colliding.IssueA, colliding.IssueB)
	}
	if strings.Join(colliding.Lines, ",") != "40-60" {
		t.Errorf("board.go lines = %v, want the intersection 40-60", colliding.Lines)
	}

	disjoint := byFile["cmd/gate/board.go"]
	if len(disjoint.Lines) != 0 {
		t.Errorf("command board.go lines = %v, want a shared file with no line collision", disjoint.Lines)
	}

	binary := byFile["assets/logo.png"]
	if len(binary.Lines) != 0 || binary.File != "assets/logo.png" {
		t.Errorf("logo.png = %+v, want a file touched with no spans", binary)
	}
}

// TestCollideOrderIsStable pins the report's determinism: the same lanes in another order
// print the same bytes, which is what lets a coordinator diff one board against the next.
func TestCollideOrderIsStable(t *testing.T) {
	a := Files{Issue: "440", Name: "chore/440", Changed: Ranges{"x.go": {{30, 40}}, "y.go": {{1, 2}}}}
	b := Files{Issue: "438", Name: "chore/438", Changed: Ranges{"x.go": {{30, 40}}, "y.go": {{9, 9}}}}

	first := Collide([]Files{a, b}, points, nil)
	second := Collide([]Files{b, a}, points, nil)
	if len(first.Overlaps) != len(second.Overlaps) {
		t.Fatalf("overlap counts differ: %d vs %d", len(first.Overlaps), len(second.Overlaps))
	}
	for i := range first.Overlaps {
		if !sameOverlap(first.Overlaps[i], second.Overlaps[i]) {
			t.Errorf("overlap %d differs between orders:\n%+v\n%+v", i, first.Overlaps[i], second.Overlaps[i])
		}
	}
}

// sameOverlap compares two overlaps field by field, because a span list is not comparable.
func sameOverlap(a, b Overlap) bool {
	return a.A == b.A && a.B == b.B && a.IssueA == b.IssueA && a.IssueB == b.IssueB &&
		a.File == b.File && a.Serialization == b.Serialization && strings.Join(a.Lines, ",") == strings.Join(b.Lines, ",")
}

// TestCollideEmpty pins the report a coordinator reads when a wave is disjoint: every
// serialization point is listed untouched, and there is nothing to resolve.
func TestCollideEmpty(t *testing.T) {
	report := Collide([]Files{
		{Issue: "438", Name: "chore/438", Changed: Ranges{"internal/build/board/board.go": {{1, 2}}}},
		{Issue: "440", Name: "chore/440", Changed: Ranges{"internal/build/board/collide.go": {{1, 2}}}},
	}, points, nil)
	if len(report.Overlaps) != 0 || report.Conflicting() {
		t.Errorf("report = %+v, want no overlap between disjoint lanes", report)
	}
	if len(report.Points) != len(points) {
		t.Errorf("got %d points, want every named point listed even untouched", len(report.Points))
	}
}

// TestFilesPaths pins the sorted path listing the report walks.
func TestFilesPaths(t *testing.T) {
	files := Files{Changed: Ranges{"b.go": nil, "a.go": nil, "CHANGELOG.md": nil}}
	if got := strings.Join(files.Paths(), ","); got != "CHANGELOG.md,a.go,b.go" {
		t.Errorf("Paths = %q, want the sorted paths", got)
	}
}
