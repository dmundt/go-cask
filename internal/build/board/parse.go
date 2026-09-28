package board

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseIssues reads `gh issue list --state open --json number,title,labels,url`. The listing
// is the spine of the board: what is actually outstanding, rather than what a lane says it
// is working on (docs/specs/coordination.md §2).
func ParseIssues(raw string) ([]Issue, error) {
	var listed []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		return nil, fmt.Errorf("reading the issue listing: %w", err)
	}
	issues := make([]Issue, 0, len(listed))
	for _, entry := range listed {
		issue := Issue{Number: entry.Number, Title: entry.Title, URL: entry.URL}
		for _, label := range entry.Labels {
			if label.Name != "" {
				issue.Labels = append(issue.Labels, label.Name)
			}
		}
		issues = append(issues, issue)
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}

// ParsePullRequests reads the listing fields the board joins on. Each entry needs a number
// and a head branch: an entry missing either names no lane and is dropped rather than
// guessed at.
func ParsePullRequests(raw string) ([]PullRequest, error) {
	var listed []struct {
		Number           int    `json:"number"`
		Title            string `json:"title"`
		HeadRefName      string `json:"headRefName"`
		HeadRefOID       string `json:"headRefOid"`
		IsDraft          bool   `json:"isDraft"`
		Mergeable        string `json:"mergeable"`
		MergeStateStatus string `json:"mergeStateStatus"`
		AutoMergeRequest *struct {
			EnabledAt string `json:"enabledAt"`
		} `json:"autoMergeRequest"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		return nil, fmt.Errorf("reading the pull-request listing: %w", err)
	}
	pulls := make([]PullRequest, 0, len(listed))
	for _, entry := range listed {
		if entry.Number == 0 || entry.HeadRefName == "" {
			continue
		}
		pulls = append(pulls, PullRequest{
			Number:     entry.Number,
			Title:      entry.Title,
			Branch:     entry.HeadRefName,
			HeadOID:    entry.HeadRefOID,
			Draft:      entry.IsDraft,
			AutoMerge:  entry.AutoMergeRequest != nil,
			MergeState: entry.MergeStateStatus,
			Mergeable:  entry.Mergeable,
		})
	}
	sort.SliceStable(pulls, func(i, j int) bool { return pulls[i].Number < pulls[j].Number })
	return pulls, nil
}

// ParseWorktrees reads `git worktree list --porcelain`: one block per registration,
// separated by a blank line, opening with `worktree <path>` and carrying `HEAD <sha>` and
// `branch refs/heads/<name>` — or `bare` / `detached`, which name no branch.
func ParseWorktrees(listing string) []Worktree {
	var entries []Worktree
	var current Worktree
	flush := func() {
		if current.Path != "" {
			entries = append(entries, current)
		}
		current = Worktree{}
	}
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		}
	}
	flush()
	return entries
}

// ParseClaimRecord reads this checkout's own record of the lanes it claimed: one issue
// number per line, blank lines ignored.
//
// The record is the checkout's note of what it started, and it is evidence of nothing on the
// remote (docs/specs/coordination.md §3): the board reads it to answer "did this checkout
// start the landing", never "does the lane exist".
func ParseClaimRecord(raw string) []string {
	var issues []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			issues = append(issues, line)
		}
	}
	return issues
}

// Ranges is the set of new-side line spans one file's diff changes, in the order the hunks
// appear. An empty slice means the file is touched without any line span this reader could
// see — a binary, a mode change, a rename — which is still a file two lanes are both editing.
type Ranges map[string][][2]int

// ParseDiff reads `git diff --unified=0` and reports, per file, the new-side line spans it
// changes. The new side is the one that matters here: two lanes are compared against a
// shared merge base, and what a hunk will occupy in the merged result is its new range.
func ParseDiff(diff string) Ranges {
	ranges := Ranges{}
	path := ""
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "+++ "):
			path = parsePlusPath(line)
			if path != "" {
				if _, seen := ranges[path]; !seen {
					ranges[path] = nil
				}
			}
		case strings.HasPrefix(line, "@@") && path != "":
			if span, ok := parseHunk(line); ok {
				ranges[path] = append(ranges[path], span)
			}
		}
	}
	return ranges
}

// parsePlusPath reads a `+++ b/<path>` line. `/dev/null` is the deleted side of a removal,
// and a path with no `b/` prefix (one produced outside a repository) is left as git printed
// it rather than dropped: a file this reader cannot name is still a file a lane touched.
func parsePlusPath(line string) string {
	value := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
	if value == "/dev/null" || value == "" {
		return ""
	}
	if quoted, err := strconv.Unquote(value); err == nil {
		value = quoted
	}
	if rest, found := strings.CutPrefix(value, "b/"); found {
		return rest
	}
	return value
}

// parseHunk reads the `@@ -a,b +c,d @@` header of a unified diff hunk and reports its new
// side as `[first, last]`. A hunk that adds nothing (`+c,0` or a bare `+c`) occupies no line,
// so it is reported as an empty span: it collides with nothing by line, but the file it is in
// is still a file two lanes are both editing.
func parseHunk(line string) ([2]int, bool) {
	closing := strings.Index(line[2:], "@@")
	if closing < 0 {
		return [2]int{}, false
	}
	header := line[2 : 2+closing]
	var newPart string
	for _, field := range strings.Fields(header) {
		if strings.HasPrefix(field, "+") {
			newPart = strings.TrimPrefix(field, "+")
		}
	}
	if newPart == "" {
		return [2]int{}, false
	}
	count := 1
	first := newPart
	if comma := strings.Index(newPart, ","); comma >= 0 {
		first = newPart[:comma]
		parsed, err := strconv.Atoi(newPart[comma+1:])
		if err != nil {
			return [2]int{}, false
		}
		count = parsed
	}
	start, err := strconv.Atoi(first)
	if err != nil {
		return [2]int{}, false
	}
	if count <= 0 {
		return [2]int{}, true
	}
	return [2]int{start, start + count - 1}, true
}

// ParseClaimed reads the moment a claim record was written, in the timestamp form the
// GitHub API prints (`tagger.date`): RFC 3339.
func ParseClaimed(value string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// MinutesBetween is how long ago a moment was, in whole minutes, never negative: a record
// stamped in the future is a clock that moved, not a claim that is fresh by an unknown
// amount, and reporting a negative age would make the window arithmetic meaningless.
func MinutesBetween(from, to time.Time) int {
	minutes := int(to.Sub(from).Minutes())
	if minutes < 0 {
		return 0
	}
	return minutes
}

// Mentioned reports whether a body names an issue the way a landing does. The check is
// deliberately literal: a pull request that says "Refs #438" is not a landing, and reading
// intent into a body is how a coordinator closes an issue nothing closed. The issue number
// must end where it ends — "Closes #4380" cites no issue 438.
func Mentioned(body, issue string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"closes #", "fixes #"} {
		needle := marker + issue
		for at := strings.Index(lower, needle); at >= 0; at = strings.Index(lower[at+1:], needle) {
			end := at + len(needle)
			if end >= len(lower) || lower[end] < '0' || lower[end] > '9' {
				return true
			}
		}
	}
	return false
}

// CanonicalPath normalizes a changed path for comparison: git spells paths with forward
// slashes on every platform, a listing may carry a trailing carriage return, and a declared
// directory is spelled with or without its trailing slash depending on who wrote it.
func CanonicalPath(path string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(path), "\r"), "/")
}

// PathsIn reads a newline-separated file list, the shape `git diff --name-only` prints.
func PathsIn(listed string) []string {
	var paths []string
	for _, line := range strings.Split(listed, "\n") {
		if path := CanonicalPath(line); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}
