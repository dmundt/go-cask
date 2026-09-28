package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/taskstate"
)

// runTaskStatus reports which branches carry work that no pull request tracks.
//
// It is the join nothing used to make: `git worktree list` knows the worktrees, `git rev-list
// --count` knows how far a branch has drifted, `gh` knows the pull requests and `pr-lane`
// knows the claims, but nothing answered "which branch carries commits that are not on the base
// ref and that no open pull request will land". The audit that wrote this found seven branches
// in that state, one of them holding the fix for the only failure blocking an open pull request.
//
// It is read-only and always exits 0, except when a reading it needs fails: it reports, and the
// judgement belongs to the session that reads the report. Finding nothing is a result, not an
// error — a command that failed when the work were landed would be one nobody runs.
func runTaskStatus(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("task-status", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}
	table := policy.Tasks()

	branches, err := localBranches()
	if err != nil {
		return err
	}
	branches, err = withDrift(branches, table.Base)
	if err != nil {
		return err
	}
	requests, err := listedPullRequests(table.PullRequestLimit)
	if err != nil {
		return err
	}
	held, err := worktreeBranches()
	if err != nil {
		return err
	}
	for i := range branches {
		if request, ok := requests[branches[i].Name]; ok {
			found := request
			branches[i].PullRequest = &found
		}
		if dirty, ok := held[branches[i].Name]; ok {
			branches[i].Worktree = true
			branches[i].Dirty = dirty
		}
	}

	renderTaskBranches(out, branches, table.Base)
	renderTaskFindings(out, taskstate.Findings(branches))
	return nil
}

// localBranches lists the repository's local branches by short name, with each one's commit.
func localBranches() ([]taskstate.Branch, error) {
	listed, err := runGit("for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []taskstate.Branch
	for _, name := range strings.Fields(listed) {
		head, err := runGit("rev-parse", "--short", name)
		if err != nil {
			return nil, err
		}
		branches = append(branches, taskstate.Branch{Name: name, Head: strings.TrimSpace(head)})
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Name < branches[j].Name })
	return branches, nil
}

// withDrift fills in how far each branch has drifted from the base ref, which is what decides
// whether its commits are landed.
func withDrift(branches []taskstate.Branch, base string) ([]taskstate.Branch, error) {
	for i := range branches {
		ahead, err := runGit("rev-list", "--count", base+".."+branches[i].Name)
		if err != nil {
			return nil, err
		}
		behind, err := runGit("rev-list", "--count", branches[i].Name+".."+base)
		if err != nil {
			return nil, err
		}
		branches[i].Ahead = atoiOrZero(ahead)
		branches[i].Behind = atoiOrZero(behind)
	}
	return branches, nil
}

// listedPullRequests reads the pull requests that name a branch, in the one call the report
// makes to the network. `--state all` is deliberate: a branch whose pull request merged is
// debris to report, and one whose pull request was closed is work to look at, and neither is
// visible in an open-only listing.
func listedPullRequests(limit int) (map[string]taskstate.PullRequest, error) {
	raw, err := runGitHubCLI("pr", "list", "--state", "all", "--limit", strconv.Itoa(limit),
		"--json", "headRefName,number,state")
	if err != nil {
		return nil, err
	}
	return parsePullRequests(raw)
}

// parsePullRequests reads `gh pr list --json headRefName,number,state`. A branch named by more
// than one pull request keeps the first, which is the newest `gh` lists.
func parsePullRequests(raw string) (map[string]taskstate.PullRequest, error) {
	var listed []struct {
		HeadRefName string `json:"headRefName"`
		Number      int    `json:"number"`
		State       string `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		return nil, fmt.Errorf("reading the pull-request listing: %w", err)
	}
	requests := make(map[string]taskstate.PullRequest, len(listed))
	for _, entry := range listed {
		if entry.HeadRefName == "" || entry.Number == 0 {
			continue
		}
		if _, seen := requests[entry.HeadRefName]; seen {
			continue
		}
		requests[entry.HeadRefName] = taskstate.PullRequest{Number: entry.Number, State: entry.State}
	}
	return requests, nil
}

// worktreeBranches maps every branch a linked worktree holds to whether that worktree has
// uncommitted changes. A worktree whose status cannot be read is reported as held and clean
// rather than as dirty: the report does not invent work it did not see.
func worktreeBranches() (map[string]bool, error) {
	listing, err := runGit("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, entry := range parseWorktrees(listing) {
		if entry.Branch == "" {
			continue
		}
		status, err := gitOutputIn(entry.Path, "status", "--porcelain")
		held[entry.Branch] = err == nil && strings.TrimSpace(status) != ""
	}
	return held, nil
}

// worktreeEntry is one registration in `git worktree list --porcelain`.
type worktreeEntry struct {
	Path   string
	Branch string
}

// parseWorktrees reads the porcelain worktree listing: one block per registration, separated by
// a blank line, opening with `worktree <path>` and carrying `branch refs/heads/<name>` unless
// the worktree is detached or bare. Those two carry no branch, so the caller skips them — they
// cannot be a branch's worktree if they do not name one.
func parseWorktrees(listing string) []worktreeEntry {
	var entries []worktreeEntry
	var current worktreeEntry
	flush := func() {
		if current.Path != "" {
			entries = append(entries, current)
		}
		current = worktreeEntry{}
	}
	for _, line := range strings.Split(listing, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return entries
}

// renderTaskBranches prints one line per branch. It is a report, not a verdict: every branch is
// listed, whether or not it is a finding.
func renderTaskBranches(out io.Writer, branches []taskstate.Branch, base string) {
	fmt.Fprintf(out, "branches against %s (%d)\n\n", base, len(branches))
	fmt.Fprintf(out, "  %-44s %-9s %5s %6s %5s %-12s %s\n",
		"BRANCH", "HEAD", "AHEAD", "BEHIND", "DIRTY", "PR", "WORKTREE")
	for _, b := range branches {
		fmt.Fprintf(out, "  %-44s %-9s %5d %6d %5s %-12s %s\n",
			clip(b.Name, 44), b.Head, b.Ahead, b.Behind, yesNo(b.Dirty), pullRequestText(b.PullRequest), worktreeText(b))
	}
}

// renderTaskFindings prints the states worth acting on, grouped by kind, and says so plainly
// when there are none: a report that printed nothing at all would leave a reader unable to tell
// "nothing to do" from "the command did not run".
func renderTaskFindings(out io.Writer, found []taskstate.Finding) {
	fmt.Fprintln(out, "\nfindings")
	if len(found) == 0 {
		fmt.Fprintln(out, "  none: every branch either has an open pull request or holds nothing the base ref lacks")
		return
	}
	kind := taskstate.Kind("")
	for _, finding := range found {
		if finding.Kind != kind {
			kind = finding.Kind
			fmt.Fprintf(out, "\n  %s:\n", kind)
		}
		fmt.Fprintf(out, "    %-44s %s\n", clip(finding.Branch, 44), finding.Detail)
	}
}

// pullRequestText renders the report's pull-request column.
func pullRequestText(request *taskstate.PullRequest) string {
	if request == nil {
		return "—"
	}
	return request.String()
}

// worktreeText renders the report's worktree column.
func worktreeText(b taskstate.Branch) string {
	switch {
	case b.Worktree && b.Dirty:
		return "yes (dirty)"
	case b.Worktree:
		return "yes"
	default:
		return "none"
	}
}

// yesNo renders a boolean the way the report states it.
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// clip shortens a name to width, so one long branch cannot push every column out of line.
func clip(name string, width int) string {
	if len(name) <= width {
		return name
	}
	return name[:width-1] + "…"
}

// atoiOrZero reads a count git printed, treating anything unexpected as zero: the report has
// nothing better to say about a count it cannot read, and a wrong count must not become a
// finding on its own.
func atoiOrZero(text string) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
