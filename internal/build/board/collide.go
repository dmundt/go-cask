package board

import (
	"fmt"
	"sort"
	"strings"
)

// Files is one lane's change set: the files it touches and, per file, the new-side line spans
// its diff changes, both against the merge base it shares with the other lanes.
type Files struct {
	// Issue is the issue the lane claims, for the report.
	Issue string `json:"issue,omitempty"`
	// Name is the lane as a session says it — usually the branch.
	Name string `json:"name"`
	// Base and Head are the two revisions the diff compared, for the report.
	Base string `json:"base,omitempty"`
	Head string `json:"head,omitempty"`
	// Changed maps a path to its changed line spans. A path with no spans is still a file
	// the lane touched: a binary, a rename, a mode change.
	Changed Ranges `json:"changed"`
}

// Paths lists the files the lane touches, sorted, which is why the report prints the same
// matrix twice.
func (f Files) Paths() []string {
	paths := make([]string, 0, len(f.Changed))
	for path := range f.Changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// Overlap is one pair of lanes that both touch one file, with the line spans where they
// would collide. Lines is empty when the file is touched without an overlapping span — a
// binary, or two hunks far enough apart — which is a merge conflict's neighbour, not one.
type Overlap struct {
	// A and B are the two lanes' names, ordered as the caller passed them.
	A string `json:"a"`
	B string `json:"b"`
	// Issues are the two issues the lanes claim, in the same order.
	IssueA string `json:"issueA,omitempty"`
	IssueB string `json:"issueB,omitempty"`
	// File is the path both lanes touch.
	File string `json:"file"`
	// Lines are the new-side spans both lanes change, as `a-b` strings.
	Lines []string `json:"lines,omitempty"`
	// Serialization is true when the file is a serialization point
	// (docs/specs/coordination.md §4): at most one lane per wave touches each, so two lanes
	// in one conflict by construction whatever their line spans are.
	Serialization bool `json:"serialization"`
}

// Serialized is one serialization point and the lanes that touch it.
type Serialized struct {
	File string `json:"file"`
	// Lanes are the lanes that touch it, as `#issue` strings, sorted.
	Lanes []string `json:"lanes"`
	// Conflict is true when more than one lane touches it — the definition of two lanes
	// that cannot share a wave.
	Conflict bool `json:"conflict"`
}

// Collisions is the overlap report: the serialization points first, because they decide the
// wave, then the ordinary file overlaps.
type Collisions struct {
	// Points are every serialization point the table names, whether or not a lane touches
	// it, so a reader can see the constraint rather than infer it from silence.
	Points []Serialized `json:"points"`
	// Overlaps are the pairs that touch one file, serialization points excluded.
	Overlaps []Overlap `json:"overlaps"`
	// Notes are readings the caller could not make.
	Notes []string `json:"notes,omitempty"`
}

// Collide computes the file-overlap matrix over the lanes in flight. Either parameter order
// prints the same report: the pairs are ordered by lane name and the overlaps by file, so
// two runs over one set of lanes are the same bytes.
//
// The serialization points are separated from the ordinary overlaps deliberately. A pair
// sharing an unrelated file may merge cleanly and is worth watching; a pair sharing one of
// these cannot both be in a wave, and saying so is the whole point
// (docs/specs/coordination.md §4).
func Collide(lanes []Files, points []string, notes []string) Collisions {
	report := Collisions{Notes: append([]string(nil), notes...)}
	report.Points = serializationPoints(lanes, points)

	pointSet := make(map[string]bool, len(points))
	for _, point := range points {
		pointSet[CanonicalPath(point)] = true
	}
	ordered := append([]Files(nil), lanes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			for _, path := range sharedPaths(ordered[i], ordered[j]) {
				if pointSet[path] {
					continue
				}
				report.Overlaps = append(report.Overlaps, Overlap{
					A:      ordered[i].Name,
					B:      ordered[j].Name,
					IssueA: ordered[i].Issue,
					IssueB: ordered[j].Issue,
					File:   path,
					Lines:  overlapLines(ordered[i].Changed[path], ordered[j].Changed[path]),
				})
			}
		}
	}
	sort.SliceStable(report.Overlaps, func(i, j int) bool {
		if report.Overlaps[i].File != report.Overlaps[j].File {
			return report.Overlaps[i].File < report.Overlaps[j].File
		}
		if report.Overlaps[i].A != report.Overlaps[j].A {
			return report.Overlaps[i].A < report.Overlaps[j].A
		}
		return report.Overlaps[i].B < report.Overlaps[j].B
	})
	return report
}

// Conflicting reports whether any serialization point carries more than one lane — the
// binding constraint a wave planner has to resolve before anything else.
func (c Collisions) Conflicting() bool {
	for _, point := range c.Points {
		if point.Conflict {
			return true
		}
	}
	return false
}

// serializationPoints reports every point the table names, with the lanes that touch it.
// Every point is listed, including the untouched ones: a reader planning a wave needs to
// know which files the wave must serialize on, and a point absent from the report looks
// like a point that does not exist.
func serializationPoints(lanes []Files, points []string) []Serialized {
	canonical := make([]string, 0, len(points))
	for _, point := range points {
		if path := CanonicalPath(point); path != "" {
			canonical = append(canonical, path)
		}
	}
	sort.Strings(canonical)

	reported := make([]Serialized, 0, len(canonical))
	for _, path := range canonical {
		point := Serialized{File: path}
		for _, lane := range lanes {
			if _, touched := lane.Changed[path]; touched {
				point.Lanes = append(point.Lanes, laneLabel(lane))
			}
		}
		sort.Strings(point.Lanes)
		point.Conflict = len(point.Lanes) > 1
		reported = append(reported, point)
	}
	return reported
}

// sharedPaths lists the files two lanes both touch, sorted.
func sharedPaths(a, b Files) []string {
	var shared []string
	for path := range a.Changed {
		if _, both := b.Changed[path]; both {
			shared = append(shared, path)
		}
	}
	sort.Strings(shared)
	return shared
}

// overlapLines renders the spans two lanes both change, as the intersection of their own
// spans. An empty result is meaningful: the file is shared, the lines are not.
func overlapLines(a, b [][2]int) []string {
	var lines []lineSpan
	for _, left := range a {
		if left[1] < left[0] {
			continue
		}
		for _, right := range b {
			if right[1] < right[0] {
				continue
			}
			first := left[0]
			if right[0] > first {
				first = right[0]
			}
			last := left[1]
			if right[1] < last {
				last = right[1]
			}
			if first <= last {
				lines = append(lines, lineSpan{first, last})
			}
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].first != lines[j].first {
			return lines[i].first < lines[j].first
		}
		return lines[i].last < lines[j].last
	})
	rendered := make([]string, 0, len(lines))
	for _, span := range lines {
		rendered = append(rendered, fmt.Sprintf("%d-%d", span.first, span.last))
	}
	return dedupe(rendered)
}

// lineSpan is one new-side span two lanes both change.
type lineSpan struct{ first, last int }

// dedupe drops repeated strings, keeping the first occurrence of each.
func dedupe(values []string) []string {
	if len(values) < 2 {
		return values
	}
	kept := values[:1]
	for _, value := range values[1:] {
		if value != kept[len(kept)-1] {
			kept = append(kept, value)
		}
	}
	return kept
}

// laneLabel names a lane in a report: its issue when it has one, else its name.
func laneLabel(lane Files) string {
	if strings.TrimSpace(lane.Issue) != "" {
		return "#" + strings.TrimPrefix(lane.Issue, "#")
	}
	return lane.Name
}
