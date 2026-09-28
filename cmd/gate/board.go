package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/internal/build/board"
	"github.com/dmundt/go-cask/internal/build/claim"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// The three coordinator verbs live in one file because they are one job: the board says what
// is in flight, collisions says which lanes cannot share a wave, and verify-landing says
// whether a lane that finished really did (docs/specs/coordination.md §2, §4, §6).
//
// All three read and none of them writes: a coordinator assembles the wave, the lanes land
// it, and a command that changed the tree while reporting on it would be another lane in the
// wave it is trying to arrange (coordination.md §1).

// The three verbs' own help, each also its usage error.
const (
	boardUsage         = "usage: gate board [--json]"
	collisionsUsage    = "usage: gate collisions [--json]"
	verifyLandingUsage = "usage: gate verify-landing <issue|pr> [--json]"
)

// runBoard prints the coordinator's board: every open issue with the lane that claims it, who
// holds it, where its worktree is, the pull request behind it, whether its head is gated
// evidence, and the one thing to do next.
func runBoard(args []string, out, errOut io.Writer) error {
	return boardCommand(args, out, errOut, productionPRLaneDeps())
}

// boardCommand is the board verb with its collaborators injected.
func boardCommand(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, boardUsage)
		return nil
	}
	asJSON, err := readJSONFlag(args, "board")
	if err != nil {
		return err
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}
	var notes []string

	issues, err := ghIssues(deps, repo)
	if err != nil {
		return err
	}
	pulls, err := ghPullRequests(deps, repo)
	if err != nil {
		return fmt.Errorf("reading the open pull requests: %w", err)
	}
	// The lane refs are the coordination markers. A listing that cannot be read is the one
	// failure the board cannot report around: every row would then look unclaimed.
	refs, err := ghLaneRefs(deps, repo)
	if err != nil {
		return fmt.Errorf("reading the lane refs: %w", err)
	}

	lanes := map[int]*board.Lane{}
	unreadable := map[int]bool{}
	for issue := range refs {
		read := prLaneRead(deps, repo, strconv.Itoa(issue))
		lane := &board.Lane{
			Ref:     policy.PRLane().RefPrefix + strconv.Itoa(issue),
			Object:  read.Object,
			Message: read.Message,
		}
		if read.ClaimedKnown {
			age := board.MinutesBetween(read.Claimed, deps.now())
			lane.AgeMinutes = &age
		}
		if read.Present && !read.Readable {
			unreadable[issue] = true
			notes = append(notes, fmt.Sprintf("lane #%d: the claim record exists but cannot be read", issue))
		}
		lanes[issue] = lane
	}

	where, worktreeErr := boardWorktrees(deps, pulls, lanes)
	if worktreeErr != nil {
		notes = append(notes, fmt.Sprintf("the worktree list could not be read (%v)", worktreeErr))
	}

	window := int(claimWindow().Minutes())
	assembled := board.BoardOf(issues, lanes, byIssue(pulls), where, unreadable, gatedCommits(commonGitDir(), pulls), notes, window)

	if asJSON {
		return writeJSON(out, assembled)
	}
	renderBoard(out, assembled, window)
	return nil
}

// readJSONFlag reads a verb's only flag, and refuses everything else: these commands take no
// arguments, so a stray word is the caller's mistake rather than something to ignore.
func readJSONFlag(args []string, verb string) (bool, error) {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		default:
			return false, usageError{fmt.Sprintf("unknown argument %q (gate %s takes --json alone)", arg, verb)}
		}
	}
	return asJSON, nil
}

// wantsHelp reports whether a verb was asked for its own help, which every verb answers
// before it reads anything.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

// ghIssues reads the open issues, which is the board's spine (coordination.md §2).
func ghIssues(deps prLaneDeps, repo string) ([]board.Issue, error) {
	raw, err := deps.gh("issue", "list", "--repo", repo, "--state", "open", "--limit", "200",
		"--json", "number,title,labels,url")
	if err != nil {
		return nil, fmt.Errorf("reading the open issues: %w", err)
	}
	issues, err := board.ParseIssues(raw)
	if err != nil {
		return nil, err
	}
	return issues, nil
}

// ghPullRequests reads the open pull requests with the two fields a board may not omit —
// draft and the auto-merge request — plus the head commit a receipt would name.
func ghPullRequests(deps prLaneDeps, repo string) ([]board.PullRequest, error) {
	raw, err := deps.gh("pr", "list", "--repo", repo, "--state", "open",
		"--limit", strconv.Itoa(policy.PRLane().PullRequestLimit),
		"--json", "number,title,headRefName,headRefOid,isDraft,mergeable,mergeStateStatus,autoMergeRequest")
	if err != nil {
		return nil, err
	}
	return board.ParsePullRequests(raw)
}

// ghLaneRefs lists every lane on the remote as an issue-to-ref-object map. One ref read per
// lane is what makes the claim records visible, and the API is asked for each by name so a
// single unreadable record cannot hide the rest.
func ghLaneRefs(deps prLaneDeps, repo string) (map[int]string, error) {
	table := policy.PRLane()
	raw, err := deps.gh("api", fmt.Sprintf("repos/%s/git/matching-refs/%s", repo, table.Namespace),
		"--jq", `.[] | "\(.ref) \(.object.sha)"`)
	if err != nil {
		return nil, err
	}
	refs := map[int]string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		issue, ok := claim.IssueOf(fields[0], table.RefPrefix)
		if !ok {
			continue
		}
		number, err := strconv.Atoi(issue)
		if err != nil {
			continue
		}
		refs[number] = fields[1]
	}
	return refs, nil
}

// byIssue indexes the open pull requests by the issue their branch names. A pull request
// whose branch names no issue in this repository's branch namespace is not a lane's lease,
// and the board does not invent one.
func byIssue(pulls []board.PullRequest) map[int]*board.PullRequest {
	index := map[int]*board.PullRequest{}
	for i := range pulls {
		issue := issueOfBranch(pulls[i].Branch)
		if issue == 0 {
			continue
		}
		if _, seen := index[issue]; seen {
			continue
		}
		pull := pulls[i]
		index[issue] = &pull
	}
	return index
}

// issueOfBranch reads the issue number a branch names. go-cask's branch namespace carries it
// (docs/specs/branch-naming.md §2), and `claim.BranchNamesIssue` is the one reader of it, so
// the board and `pr-lane` can never disagree about which pull request holds a lane.
func issueOfBranch(branch string) int {
	if branch == "" {
		return 0
	}
	for _, issue := range issueCandidates(branch) {
		if claim.BranchNamesIssue(branch, issue) {
			number, err := strconv.Atoi(issue)
			if err == nil {
				return number
			}
		}
	}
	return 0
}

// issueCandidates lists the digit runs a branch name carries. A branch names its issue as one
// of them (`chore/438-gate-board`), and which one it is is `claim`'s rule to decide.
func issueCandidates(branch string) []string {
	var candidates []string
	current := strings.Builder{}
	for _, r := range branch {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			continue
		}
		if current.Len() > 0 {
			candidates = append(candidates, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		candidates = append(candidates, current.String())
	}
	return candidates
}

// boardWorktrees reads `git worktree list --porcelain` and counts, for each lane's branch,
// how far it is ahead of the base ref — the reading that separates a live session from an
// abandoned claim (coordination.md §7). A count that cannot be read is left unknown, so no
// claim is ever called abandoned on the strength of a reading nobody made.
func boardWorktrees(deps prLaneDeps, pulls []board.PullRequest, lanes map[int]*board.Lane) (map[string]board.Where, error) {
	listed, err := deps.git("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	base, baseErr := prLaneBase(deps)
	where := board.WorktreesOf(board.ParseWorktrees(listed))

	branches := map[string]bool{}
	for _, pull := range pulls {
		branches[pull.Branch] = true
	}
	for _, lane := range lanes {
		if branch := branchOfClaim(lane.Message); branch != "" {
			branches[branch] = true
		}
	}
	for branch := range branches {
		if branch == "" {
			continue
		}
		spot := where[branch]
		if baseErr != nil {
			where[branch] = board.Where{Path: spot.Path}
			continue
		}
		counted, countErr := deps.git("rev-list", "--count", base+".."+branch)
		if countErr != nil {
			where[branch] = board.Where{Path: spot.Path}
			continue
		}
		where[branch] = board.Where{Path: spot.Path, Unpushed: atoiOrZero(counted), Known: true}
	}
	return where, nil
}

// branchOfClaim reads the branch a claim record's message names.
func branchOfClaim(message string) string {
	for _, field := range strings.Fields(message) {
		if value, found := strings.CutPrefix(field, "branch="); found {
			return value
		}
	}
	return ""
}

// gatedCommits reads, for each open pull request's head, whether the clone ledger names that
// commit and a receipt for it exists. Both are required: one without the other is not the
// evidence a green gate leaves (coordination.md §3).
func gatedCommits(common string, pulls []board.PullRequest) map[string]bool {
	commits := map[string]bool{}
	if common == "" {
		return commits
	}
	ledger := readLedger(filepath.Join(common, policy.Gate().Ledger))
	for _, pull := range pulls {
		if pull.HeadOID == "" {
			continue
		}
		if _, seen := commits[pull.HeadOID]; seen {
			continue
		}
		commits[pull.HeadOID] = ledger[strings.ToLower(pull.HeadOID)] && receiptExists(common, pull.HeadOID)
	}
	return commits
}

// commonGitDir resolves the clone's common git directory, which is where the ledger and the
// receipts live whether the caller is the primary checkout or a linked worktree
// (coordination.md §3).
func commonGitDir() string {
	dir, err := gitPathIn("", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return dir
}

// readLedger reads the verified-commit ledger into a set of full commit names. The gate
// writes it; a line that is not a commit name is not a verification.
func readLedger(path string) map[string]bool {
	verified := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return verified
	}
	for _, line := range strings.Split(string(data), "\n") {
		if sha := strings.TrimSpace(line); isCommitName(sha) {
			verified[strings.ToLower(sha)] = true
		}
	}
	return verified
}

// isCommitName reports whether a ledger line is a full commit name.
func isCommitName(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, digit := range sha {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return false
		}
	}
	return true
}

// receiptExists reports whether a receipt file names a commit.
func receiptExists(common, sha string) bool {
	_, err := os.Stat(filepath.Join(common, "gate-receipts", strings.ToLower(sha)+".receipt"))
	return err == nil
}

// renderBoard prints the board as a table, then its caveats. Every column names its own
// authority, and NEXT is the one step the row calls for — the decision the coordinator would
// otherwise assemble by hand from the other columns.
func renderBoard(out io.Writer, assembled board.Board, window int) {
	fmt.Fprintf(out, "board v%d — %d open issue(s), claim window %d minutes\n\n",
		board.Version, len(assembled.Rows), window)
	fmt.Fprintf(out, "  %-7s %-13s %-22s %-24s %-8s %-10s %-10s %s\n",
		"ISSUE", "CLAIM", "HOLDER", "PR", "GATED", "CLAIM", "WORKTREE", "NEXT")
	for _, row := range assembled.Rows {
		fmt.Fprintf(out, "  %-7s %-13s %-22s %-24s %-8s %-10s %-10s %s\n",
			"#"+strconv.Itoa(row.Issue.Number), claimText(row), clip(row.Holder, 22),
			pullText(row.PullRequest), gatedText(row), string(row.Claim), clip(worktreeName(row.Worktree), 10),
			row.Next)
	}
	if len(assembled.Notes) > 0 {
		fmt.Fprintln(out, "\nnotes")
		for _, note := range assembled.Notes {
			fmt.Fprintf(out, "  %s\n", note)
		}
	}
}

// claimText renders the claim column: the ref is the claim, and the age in minutes is when it
// was made — the one number the 90-minute window is measured against.
func claimText(row board.Row) string {
	if row.Lane == nil {
		return "—"
	}
	name := strings.TrimPrefix(row.Lane.Ref, "refs/lane/")
	if row.Lane.AgeMinutes != nil {
		return fmt.Sprintf("%s (%dm)", name, *row.Lane.AgeMinutes)
	}
	return name
}

// pullText renders the lease column: the number, the merge state, the draft flag and whether
// a merge is armed — because a green draft and a green armed pull request are
// indistinguishable without them (coordination.md §2).
func pullText(pull *board.PullRequest) string {
	if pull == nil {
		return "—"
	}
	suffix := ""
	if pull.Draft {
		suffix += " draft"
	}
	if pull.AutoMerge {
		suffix += " armed"
	}
	return fmt.Sprintf("#%d %s%s", pull.Number, stateText(pull.MergeState), suffix)
}

// stateText renders a merge state, or `?` when the forge has not decided.
func stateText(state string) string {
	if state == "" {
		return "?"
	}
	return state
}

// gatedText renders the evidence column: whether the clone ledger and a receipt both name the
// pull request's head. It reports evidence, never a lane's claim of it (coordination.md §3).
func gatedText(row board.Row) string {
	switch {
	case row.PullRequest == nil || row.PullRequest.HeadOID == "":
		return "—"
	case row.Gated:
		return "receipt"
	default:
		return "none"
	}
}

// worktreeName shortens a worktree path to its directory name, which is what a session and a
// claim record are identified by.
func worktreeName(path string) string {
	if path == "" {
		return "—"
	}
	return filepath.Base(path)
}

// writeJSON renders a report as the JSON a script reads, with a trailing newline so a shell
// caller can capture it whole.
func writeJSON(out io.Writer, value any) error {
	rendered, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering the report: %w", err)
	}
	fmt.Fprintf(out, "%s\n", rendered)
	return nil
}

// prLaneIssueArg reads the issue or pull request a verb was given: one positive number,
// optionally written with the `#` a human types.
func prLaneIssueArg(value string) (int, error) {
	digits := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if digits == "" {
		return 0, usageError{"an issue or pull request number is required"}
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number <= 0 {
		return 0, usageError{fmt.Sprintf("an issue or pull request number is required (got %q)", value)}
	}
	return number, nil
}

// claimWindow is the window a claim with no pull request is honoured for, read through the
// same policy the lane commands use so the board's staleness reading cannot drift from the
// one `pr-lane` acts on.
func claimWindow() time.Duration {
	return prLaneWindow(policy.PRLane())
}

// serializationPoints are the files at most one lane per wave may touch
// (docs/specs/coordination.md §4). They are stated here rather than in
// `internal/build/policy` because the engine's table belongs to go-cask's policy package and
// this change is not allowed to write it: a coordinator moves this list to
// `policy.Serializations()` and passes it in, changing the engine call not at all.
var serializationPoints = []string{
	"CHANGELOG.md",
	"docs/index.md",
	"docs/design/package-graph.md",
	"docs/specs/defaults.md",
	"docs/specs/cas-core.md",
	"docs/specs/landing.md",
	"internal/build/policy/policy.go",
}

// runCollisions prints the file-overlap matrix over the lanes in flight, with the
// serialization points separated from the ordinary overlaps because they decide the wave.
func runCollisions(args []string, out, errOut io.Writer) error {
	return collisionsCommand(args, out, errOut, productionPRLaneDeps())
}

// collisionsCommand is the collisions verb with its collaborators injected.
func collisionsCommand(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, collisionsUsage)
		return nil
	}
	asJSON, err := readJSONFlag(args, "collisions")
	if err != nil {
		return err
	}
	mine, err := collisionsWorktrees(deps)
	if err != nil {
		return err
	}
	report := board.Collide(mine.files, serializationPoints, mine.notes)
	if asJSON {
		return writeJSON(out, report)
	}
	renderCollisions(out, report)
	return nil
}

// collisionsRead is the reading the collisions verb made: the lanes in flight, and what could
// not be read while assembling them.
type collisionsRead struct {
	files []board.Files
	notes []string
}

// collisionsWorktrees reads every task worktree's change set against its merge base with the
// base ref: `git merge-base` names the shared ancestor, and `git diff --unified=0` against it
// is what the lane would land on top of. The diff runs in the worktree's own directory, so a
// lane's uncommitted work counts too — the collision that costs a rebuild is the one that
// happens before the push, not after it.
func collisionsWorktrees(deps prLaneDeps) (collisionsRead, error) {
	base, err := prLaneBase(deps)
	if err != nil {
		return collisionsRead{}, err
	}
	listed, err := deps.git("worktree", "list", "--porcelain")
	if err != nil {
		return collisionsRead{}, err
	}
	read := collisionsRead{}
	seen := map[string]bool{}
	for _, entry := range board.ParseWorktrees(listed) {
		if entry.Bare || entry.Detached || entry.Branch == "" || seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		files, note := laneDiff(deps, base, entry)
		if note != "" {
			read.notes = append(read.notes, note)
			continue
		}
		if len(files.Changed) == 0 {
			read.notes = append(read.notes, fmt.Sprintf("%s: no changes against %s",
				board.CanonicalPath(entry.Path), shortCommit(base)))
			continue
		}
		read.files = append(read.files, files)
	}
	return read, nil
}

// laneDiff reads one worktree's change set against the base ref's merge base with its HEAD.
// The merge base is the honest comparison: a lane that has merged main into its branch has
// moved its own diff, and only the shared ancestor shows what both lanes actually changed.
func laneDiff(deps prLaneDeps, base string, entry board.Worktree) (board.Files, string) {
	lane := board.Files{Name: branchOrName(entry), Base: base, Head: entry.Head}
	mergeBase, err := gitInWorktree(deps, entry.Path, "merge-base", base, "HEAD")
	if err != nil {
		mergeBase, err = gitInWorktree(deps, entry.Path, "merge-base", base, entry.Branch)
		if err != nil {
			return lane, fmt.Sprintf("%s: no merge base with %s (%v)", entry.Branch, shortCommit(base), err)
		}
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		return lane, fmt.Sprintf("%s: no merge base with %s", entry.Branch, shortCommit(base))
	}
	diff, err := gitInWorktree(deps, entry.Path, "diff", "--unified=0", mergeBase)
	if err != nil {
		return lane, fmt.Sprintf("%s: the diff against %s could not be read (%v)",
			entry.Branch, shortCommit(mergeBase), err)
	}
	lane.Base = mergeBase
	lane.Changed = board.ParseDiff(diff)
	return lane, ""
}

// branchOrName names a lane in the report: its branch, or the worktree's directory when the
// registration holds a detached HEAD.
func branchOrName(entry board.Worktree) string {
	if entry.Branch != "" {
		return entry.Branch
	}
	return board.CanonicalPath(filepath.Base(entry.Path))
}

// gitInWorktree runs one git query in a worktree directory, which is what makes the answer
// that worktree's own rather than the caller's. The directory goes through the injected git
// collaborator as `-C`, so a test drives the whole verb without a checkout to run in.
func gitInWorktree(deps prLaneDeps, dir string, args ...string) (string, error) {
	if dir == "" {
		return deps.git(args...)
	}
	return deps.git(append([]string{"-C", dir}, args...)...)
}

// shortCommit abbreviates a commit for a report.
func shortCommit(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "the base ref"
	}
	return sha
}

// renderCollisions prints the serialization points first — every one of them, whether or not
// a lane touches it — then the ordinary overlaps.
func renderCollisions(out io.Writer, report board.Collisions) {
	fmt.Fprintln(out, "serialization points — at most one lane per wave (docs/specs/coordination.md §4)")
	for _, point := range report.Points {
		marker := "  "
		if point.Conflict {
			marker = "!!"
		}
		lanes := strings.Join(point.Lanes, " ")
		if lanes == "" {
			lanes = "—"
		}
		fmt.Fprintf(out, "%s %-34s %s\n", marker, point.File, lanes)
	}

	fmt.Fprintf(out, "\nfile overlaps (%d)\n", len(report.Overlaps))
	if len(report.Overlaps) == 0 {
		fmt.Fprintln(out, "  none: no two lanes in flight touch one file")
	}
	for _, overlap := range report.Overlaps {
		fmt.Fprintf(out, "  %-40s %s / %s %s\n", overlap.File, laneName(overlap.A, overlap.IssueA),
			laneName(overlap.B, overlap.IssueB), lineText(overlap.Lines))
	}
	if len(report.Notes) > 0 {
		fmt.Fprintln(out, "\nnotes")
		for _, note := range report.Notes {
			fmt.Fprintf(out, "  %s\n", note)
		}
	}
}

// laneName renders a lane in the matrix: its issue when it claims one, else its branch.
func laneName(name, issue string) string {
	if strings.TrimSpace(issue) != "" {
		return "#" + strings.TrimPrefix(issue, "#")
	}
	return name
}

// lineText renders the lines a pair both changes, so a reader can tell a shared file from a
// shared hunk: a file with no overlap may still merge cleanly.
func lineText(lines []string) string {
	if len(lines) == 0 {
		return "(no shared lines)"
	}
	return "lines " + strings.Join(lines, ",")
}

// runVerifyLanding runs the six checks a landing is verified by, each read from its own
// authority (docs/specs/coordination.md §6). It takes an issue or a pull request: a
// coordinator verifying another session's landing has the issue, and a lane verifying its own
// has the pull request.
func runVerifyLanding(args []string, out, errOut io.Writer) error {
	return verifyLandingCommand(args, out, errOut, productionPRLaneDeps())
}

// verifyLandingCommand is the verify-landing verb with its collaborators injected.
func verifyLandingCommand(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, verifyLandingUsage)
		return nil
	}
	asJSON := false
	subject := ""
	for _, arg := range args {
		switch {
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return usageError{fmt.Sprintf("unknown argument %q (%s)", arg, verifyLandingUsage)}
		case subject == "":
			subject = arg
		default:
			return usageError{fmt.Sprintf("unexpected argument %q (%s)", arg, verifyLandingUsage)}
		}
	}
	number, err := prLaneIssueArg(subject)
	if err != nil {
		return err
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}

	// The argument is an issue or a pull request, as a coordinator has one or the other:
	// a lane reads its own pull request number, and a coordinator verifying someone
	// else's landing usually has the issue.
	pull, err := ghPullView(deps, repo, number)
	if err != nil {
		issue, issueErr := prLaneIssue(strconv.Itoa(number))
		if issueErr != nil {
			return err
		}
		pull, err = ghPullForIssue(deps, repo, issue)
		if err != nil {
			return fmt.Errorf("no pull request #%d, and no pull request closes issue #%s: %w", number, issue, err)
		}
	}
	verdict, err := landingVerdict(deps, repo, pull)
	if err != nil {
		return err
	}
	verification := board.Checks(verdict)
	if asJSON {
		if err := writeJSON(out, verification); err != nil {
			return err
		}
	} else {
		renderVerification(out, verification)
	}
	if !verification.Verified {
		return exitStatus(1)
	}
	return nil
}

// ghPullForIssue finds the pull request that closes an issue: the merged one when there is
// one, else the open one. The search is the forge's own reference lookup, so a coordinator
// holding only the issue number reaches the same landing its pull request number would.
func ghPullForIssue(deps prLaneDeps, repo, issue string) (pullView, error) {
	raw, err := deps.gh("pr", "list", "--repo", repo, "--state", "all", "--search", "Closes #"+issue,
		"--limit", strconv.Itoa(policy.PRLane().PullRequestLimit), "--json", "number,state")
	if err != nil {
		return pullView{}, err
	}
	var listed []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		return pullView{}, err
	}
	candidates := make([]int, 0, len(listed))
	for _, entry := range listed {
		if entry.Number > 0 {
			candidates = append(candidates, entry.Number)
		}
	}
	if len(candidates) == 0 {
		return pullView{}, fmt.Errorf("no pull request names issue #%s", issue)
	}
	// A merged pull request is the landing; failing that, the newest open one is the
	// candidate a coordinator would look at.
	var firstErr error
	for _, number := range candidates {
		pull, err := ghPullView(deps, repo, number)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if strings.EqualFold(pull.State, "MERGED") {
			return pull, nil
		}
	}
	pull, err := ghPullView(deps, repo, candidates[0])
	if err != nil {
		if firstErr != nil {
			return pullView{}, firstErr
		}
		return pullView{}, err
	}
	return pull, nil
}

// ghPullView reads one pull request with everything the six checks read from it: the state,
// the merge commit and its parents, the body, the head commit, and the issue the body cites.
func ghPullView(deps prLaneDeps, repo string, number int) (pullView, error) {
	raw, err := deps.gh("pr", "view", strconv.Itoa(number), "--repo", repo, "--json",
		"number,title,state,body,headRefName,headRefOid,mergeCommit,mergedAt,mergeStateStatus")
	if err != nil {
		return pullView{}, fmt.Errorf("reading pull request #%d: %w", number, err)
	}
	var view struct {
		Number           int    `json:"number"`
		State            string `json:"state"`
		Body             string `json:"body"`
		HeadRefName      string `json:"headRefName"`
		HeadRefOID       string `json:"headRefOid"`
		MergeStateStatus string `json:"mergeStateStatus"`
		MergeCommit      *struct {
			OID string `json:"oid"`
			// Parents is requested but the CLI may omit it; the parent count is read
			// from git instead, because it is what settles the merge method and git is
			// the authority for a commit's parents.
			Parents *[]struct {
				OID string `json:"oid"`
			} `json:"parents"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return pullView{}, fmt.Errorf("reading pull request #%d: %w", number, err)
	}
	if view.Number == 0 {
		view.Number = number
	}
	pull := pullView{
		Number:      view.Number,
		State:       view.State,
		Body:        view.Body,
		Branch:      view.HeadRefName,
		HeadOID:     view.HeadRefOID,
		MergeState:  view.MergeStateStatus,
		IssueNumber: issueCited(view.Body),
	}
	if view.MergeCommit != nil {
		pull.MergeOID = view.MergeCommit.OID
	}
	return pull, nil
}

// pullView is one pull request as the six checks need it.
type pullView struct {
	Number int
	// State is the forge's state: MERGED is the only one a landing can have.
	State string
	// Body is the merged body, which must cite the issue.
	Body string
	// Branch is the head branch, which names the issue when the body does not.
	Branch string
	// HeadOID is the commit the branch head carried when the pull request existed.
	HeadOID string
	// MergeOID is the commit the forge says the pull request merged as. Its parent count is
	// what settles the merge method, read from git by `squashMerged`.
	MergeOID string
	// MergeState is the forge's mergeStateStatus.
	MergeState string
	// IssueNumber is the issue the body cites, 0 when it cites none.
	IssueNumber int
}

// squashMerged reads the merge method from the merge commit's parents, which `git rev-list
// --parents -1` reports: one parent is a squash, and two are a merge commit, which
// `landing.md` refuses. Git is the authority rather than the forge's commit view, because the
// parent count is what a squash IS; when git cannot say, the method stays unproven —
// guessing it is how a merge commit passes a check that refuses one.
func (p pullView) squashMerged(deps prLaneDeps) bool {
	if p.MergeOID == "" {
		return false
	}
	out, err := deps.git("rev-list", "--parents", "-1", p.MergeOID)
	if err != nil {
		return false
	}
	return len(strings.Fields(strings.TrimSpace(out))) == 2
}

// issueCited reads the issue number out of a `Closes #NNN` citation. An empty result is
// reported rather than guessed: the pull request's branch names an issue too, but a body that
// does not close it is exactly the drift §6's first line is looking for.
func issueCited(body string) int {
	lower := strings.ToLower(body)
	for _, marker := range []string{"closes #", "fixes #"} {
		for at := strings.Index(lower, marker); at >= 0; at = strings.Index(lower[at+1:], marker) {
			rest := lower[at+len(marker):]
			digits := rest
			if end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
				digits = rest[:end]
			}
			if number, err := strconv.Atoi(digits); err == nil && number > 0 {
				return number
			}
			if len(rest) == 0 {
				break
			}
		}
	}
	return 0
}

// landingVerdict reads the six checks' inputs, each from its own authority. A reading that
// fails is a note on the report, never a silent pass: a line whose evidence could not be read
// must fail, or the command would verify landings it never looked at.
func landingVerdict(deps prLaneDeps, repo string, pull pullView) (board.Verdict, error) {
	verdict := board.Verdict{
		Subject:    fmt.Sprintf("PR #%d", pull.Number),
		Merged:     strings.EqualFold(pull.State, "MERGED"),
		MergeState: pull.MergeState,
		Squash:     pull.squashMerged(deps),
		Body:       pull.Body,
		Head:       pull.HeadOID,
	}

	issue := pull.IssueNumber
	if issue == 0 {
		issue = issueOfBranch(pull.Branch)
		verdict.Notes = append(verdict.Notes,
			fmt.Sprintf("the merged body cites no issue; reading issue #%d from the head branch instead", issue))
	}
	verdict.Issue = strconv.Itoa(issue)

	if issue != 0 {
		state, body, err := ghIssueState(deps, repo, issue)
		if err != nil {
			verdict.Notes = append(verdict.Notes, fmt.Sprintf("issue #%d could not be read: %v", issue, err))
		} else {
			verdict.IssueState = state
			verdict.Scope = board.ScopeIn(body)
			if len(verdict.Scope) == 0 {
				verdict.Notes = append(verdict.Notes, fmt.Sprintf("issue #%d declares no scope", issue))
			}
		}
	}

	// The changed files are the merge commit's diff, not the branch's: what actually landed
	// is what §6 asks about, and the two differ whenever main moved under the branch.
	head := pull.HeadOID
	if pull.MergeOID != "" {
		head = pull.MergeOID
	}
	if head != "" {
		listed, err := deps.git("show", "--name-only", "--format=", head)
		if err != nil {
			verdict.Notes = append(verdict.Notes, fmt.Sprintf("the file list of %s could not be read: %v", shortCommit(head), err))
		} else {
			verdict.Files = board.PathsIn(listed)
		}
	}

	verdict.UserVisible = false
	for _, file := range verdict.Files {
		if !strings.HasSuffix(file, ".md") || file == "CHANGELOG.md" {
			verdict.UserVisible = true
		}
	}
	verdict.Changelog = containsPath(verdict.Files, "CHANGELOG.md")
	verdict.VersionBumped = versionBumped(deps, verdict.Files, head)

	if pull.HeadOID != "" {
		_, signedErr := deps.git("verify-commit", pull.HeadOID)
		verdict.Signed = signedErr == nil
		verdict.ReceiptPublished = refOnOrigin(deps, gateRefPrefix+"/"+pull.HeadOID)
	}
	if verdict.Head == "" {
		verdict.Notes = append(verdict.Notes, "the pull request reports no head commit: the signature and the receipt cannot be checked")
	}

	// The lane is released when the claim ref is gone, and the worktree is gone when
	// `git worktree list` — the authority for a removal (§3) — no longer names it.
	if issue != 0 {
		ref := policy.PRLane().RefPrefix + strconv.Itoa(issue)
		verdict.LaneReleased = !refOnOrigin(deps, ref)
		verdict.WorktreeGone = !worktreeNamed(deps, issue)
	}

	return verdict, nil
}

// gateRefPrefix is the published form of a gate receipt: `refs/gate/<sha>` on origin, which
// is the one CI reads (docs/specs/coordination.md §3).
const gateRefPrefix = "refs/gate"

// refOnOrigin reports whether a ref exists on origin, by running `ls-remote` for that one ref
// and reading the object name it prints, so the answer is git's own rather than a substring
// of a listing.
func refOnOrigin(deps prLaneDeps, ref string) bool {
	out, err := deps.git("ls-remote", "origin", ref)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == ref {
			return true
		}
	}
	return false
}

// worktreeNamed reports whether `git worktree list` still names a worktree for an issue. It
// matches the worktree's branch or its directory name, because a claim is released before its
// worktree is removed and both spellings appear in the two records a coordinator reads.
func worktreeNamed(deps prLaneDeps, issue int) bool {
	listed, err := deps.git("worktree", "list", "--porcelain")
	if err != nil {
		// A listing that cannot be read is not a removal. The check fails, because the
		// authority for a removal said nothing.
		return true
	}
	marker := strconv.Itoa(issue)
	for _, entry := range board.ParseWorktrees(listed) {
		if claim.BranchNamesIssue(entry.Branch, marker) || strings.Contains(entry.Path, "wt-"+marker) {
			return true
		}
	}
	return false
}

// ghIssueState reads an issue's state and body: the second check's authority, and the scope
// the third check reads.
func ghIssueState(deps prLaneDeps, repo string, issue int) (string, string, error) {
	raw, err := deps.gh("issue", "view", strconv.Itoa(issue), "--repo", repo, "--json", "state,body")
	if err != nil {
		return "", "", err
	}
	var view struct {
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return "", "", fmt.Errorf("reading issue #%d: %w", issue, err)
	}
	return view.State, view.Body, nil
}

// containsPath reports whether a changed-file list names a path.
func containsPath(files []string, path string) bool {
	for _, file := range files {
		if file == path {
			return true
		}
	}
	return false
}

// versionBumped reports whether the landing moved a frontmatter `version:` in any versioned
// Markdown file it changed. The rule is `versioning`'s and the schema is `docs`'s; this reads
// the landing's own diff for the one line both agree on, which keeps the command from
// deciding a rule the engine already owns.
func versionBumped(deps prLaneDeps, files []string, head string) bool {
	if head == "" {
		return false
	}
	for _, file := range files {
		if !strings.HasSuffix(file, ".md") {
			continue
		}
		// `HEAD^..HEAD` would read the wrong commit: the caller's HEAD is the checkout's,
		// and the landing is the commit being verified.
		diff, err := deps.git("show", "--unified=0", "--format=", head, "--", file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(diff, "\n") {
			if strings.HasPrefix(line, "+version:") || strings.HasPrefix(line, "-version:") {
				return true
			}
		}
	}
	return false
}

// renderVerification prints the six lines, each with its own authority's evidence, so a
// report read beside docs/specs/coordination.md §6 lines up line for line.
func renderVerification(out io.Writer, verification board.Verification) {
	held := len(verification.Checks) - len(failedChecks(verification))
	fmt.Fprintf(out, "verify-landing %s — %d of %d line(s) hold\n\n",
		verification.Subject, held, len(verification.Checks))
	for _, check := range verification.Checks {
		marker := "FAIL"
		if check.OK {
			marker = "ok"
		}
		fmt.Fprintf(out, "  [%s] %s\n         %s\n", marker, check.Name, check.Evidence)
	}
	if len(verification.Notes) > 0 {
		fmt.Fprintln(out, "\nnotes")
		for _, note := range verification.Notes {
			fmt.Fprintf(out, "  %s\n", note)
		}
	}
	if !verification.Verified {
		fmt.Fprintf(out, "\nnot verified: %s\n", strings.Join(failedChecks(verification), "; "))
	}
}

// failedChecks names the lines that do not hold, for the report's closing summary.
func failedChecks(verification board.Verification) []string {
	var failed []string
	for _, check := range verification.Checks {
		if !check.OK {
			failed = append(failed, check.Name)
		}
	}
	return failed
}
