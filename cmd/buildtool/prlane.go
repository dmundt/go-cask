package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dmundt/go-cask/internal/build/core/claim"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// prLaneDeps are the lane's collaborators: the GitHub CLI, git, and the clock. They are
// fields rather than direct calls so a test can drive the protocol — the atomic claim, the
// window, the takeover — without a remote, which is what the shell harness did with a stub
// `gh` in a throwaway repository.
type prLaneDeps struct {
	// gh runs the GitHub CLI and returns its standard output. A failure carries the
	// command's own text, because two of the calls are decided by what it says: a ref that
	// already exists, and a ref that is already gone.
	gh func(args ...string) (string, error)
	// git runs git in the working directory.
	git func(args ...string) (string, error)
	// now is the clock the claim window is measured against.
	now func() time.Time
}

// productionPRLaneDeps returns the collaborators the command uses outside a test.
func productionPRLaneDeps() prLaneDeps {
	return prLaneDeps{gh: runGitHubCLI, git: runGit, now: time.Now}
}

// runGitHubCLI runs the GitHub CLI, resolving the binary the way both toolchains this
// repository is worked in can use: `PR_LANE_GH`, then `gh`, then the Windows executable and
// its usual install paths.
func runGitHubCLI(args ...string) (string, error) {
	table := policy.PRLane()
	binary := ""
	candidates := append([]string{os.Getenv(table.GhEnv)}, table.GhCandidates...)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			binary = resolved
			break
		}
	}
	if binary == "" {
		return "", errors.New("gh is required: the lane lives on the remote, not in this clone")
	}
	cmd := exec.Command(binary, args...)
	var stdout, combined strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(combined.String()))
	}
	return stdout.String(), nil
}

// runGit runs one git query in the working directory.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, combined strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// runPRLane coordinates one landing lane: the coordination ref on the remote, claimed with a
// server-side compare-and-swap, and the pull request that is the lease.
func runPRLane(args []string, out, errOut io.Writer) error {
	return prLane(args, out, errOut, productionPRLaneDeps())
}

// prLaneUsage is the command's own help, which is also its usage error.
const prLaneUsage = "usage: buildtool pr-lane [claim <issue> | check <issue> | status [<issue>] [--json] | " +
	"release <issue> [--force] | whoami]"

// prLane is the command with its collaborators injected.
func prLane(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	if len(args) == 0 {
		fmt.Fprintln(errOut, prLaneUsage)
		return usageError{prLaneUsage}
	}
	switch args[0] {
	case "claim":
		return prLaneClaim(args[1:], out, errOut, deps)
	case "check":
		return prLaneCheck(args[1:], out, errOut, deps)
	case "status":
		return prLaneStatus(args[1:], out, errOut, deps)
	case "release":
		return prLaneRelease(args[1:], out, errOut, deps)
	case "whoami":
		return prLaneWhoami(args[1:], out, errOut, deps)
	case "-h", "--help", "help":
		fmt.Fprintln(out, prLaneUsage)
		return nil
	default:
		fmt.Fprintln(errOut, prLaneUsage)
		return usageError{prLaneUsage}
	}
}

// prLaneIssue reads the issue number a command was given. A lane is named by an issue, so
// anything but digits is the caller's mistake.
func prLaneIssue(value string) (string, error) {
	if value == "" {
		return "", usageError{"an issue number is required"}
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return "", usageError{fmt.Sprintf("an issue number is required (got %q)", value)}
		}
	}
	return value, nil
}

// prLaneWindow reads the claim window, letting the environment override the table.
func prLaneWindow(table policy.PRLaneTable) time.Duration {
	if override := os.Getenv(table.StaleEnv); override != "" {
		if minutes, err := time.ParseDuration(override + "m"); err == nil && minutes >= 0 {
			return minutes
		}
	}
	return time.Duration(table.StaleMinutes) * time.Minute
}

// prLaneRepo resolves the repository the lane lives in: the override, else the origin
// remote, else what the CLI reports. Reading the remote needs no credential, which is why
// it comes first.
func prLaneRepo(deps prLaneDeps) (string, error) {
	table := policy.PRLane()
	if repo := os.Getenv(table.RepoEnv); repo != "" {
		return repo, nil
	}
	if out, err := deps.git("config", "--get", "remote.origin.url"); err == nil {
		if repo := repoFromURL(strings.TrimSpace(out)); repo != "" {
			return repo, nil
		}
	}
	if out, err := deps.gh("repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"); err == nil {
		if repo := strings.TrimSpace(out); strings.Contains(repo, "/") {
			return repo, nil
		}
	}
	return "", errors.New("cannot resolve the repository from the origin remote; set " + table.RepoEnv)
}

// repoFromURL reads `owner/name` out of an origin URL, in the two shapes git writes: the
// scheme form and the scp-like `user@host:owner/name` form.
func repoFromURL(url string) string {
	repo := ""
	switch {
	case strings.Contains(url, "://"):
		rest := url[strings.Index(url, "://")+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			repo = rest[slash+1:]
		}
	case strings.Contains(url, "@") && strings.Contains(url, ":"):
		repo = url[strings.Index(url, ":")+1:]
	}
	return strings.TrimSuffix(repo, ".git")
}

// prLaneBase is the commit a claim is recorded against: what the remote calls main, read
// with `ls-remote` so a status or a check never rewrites the caller's refs.
func prLaneBase(deps prLaneDeps) (string, error) {
	if out, err := deps.git("ls-remote", "origin", "refs/heads/main"); err == nil {
		if fields := strings.Fields(strings.TrimSpace(out)); len(fields) > 0 && fields[0] != "" {
			return fields[0], nil
		}
	}
	if out, err := deps.git("rev-parse", "origin/main"); err == nil {
		if sha := strings.TrimSpace(out); sha != "" {
			return sha, nil
		}
	}
	return "", errors.New("cannot resolve origin/main")
}

// prLaneWorktree is this checkout as a claim record names it: a bare directory name, which
// both toolchains spell the same way.
func prLaneWorktree(deps prLaneDeps) string {
	if out, err := deps.git("rev-parse", "--show-toplevel"); err == nil {
		if top := strings.TrimSpace(out); top != "" {
			return filepath.Base(top)
		}
	}
	return "primary"
}

// prLaneBranch is the branch being claimed.
func prLaneBranch(deps prLaneDeps) string {
	if out, err := deps.git("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if branch := strings.TrimSpace(out); branch != "" {
			return branch
		}
	}
	return "detached"
}

// prLaneRecordPath is this worktree's own record of the lanes it claimed.
func prLaneRecordPath(deps prLaneDeps) string {
	table := policy.PRLane()
	dir, err := deps.git("rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return table.RecordFile
	}
	return filepath.Join(strings.TrimSpace(dir), table.RecordFile)
}

// prLaneRecordAdd records an issue this worktree claimed, once.
func prLaneRecordAdd(deps prLaneDeps, issue string) error {
	path := prLaneRecordPath(deps)
	for _, recorded := range prLaneRecordList(deps) {
		if recorded == issue {
			return nil
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("recording the claim in %s: %w", path, err)
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, issue)
	return err
}

// prLaneRecordRemove drops an issue from the record, leaving the file absent rather than
// empty when it was the only one.
func prLaneRecordRemove(deps prLaneDeps, issue string) error {
	path := prLaneRecordPath(deps)
	var kept []string
	for _, recorded := range prLaneRecordList(deps) {
		if recorded != issue {
			kept = append(kept, recorded)
		}
	}
	if len(kept) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clearing %s: %w", path, err)
		}
		return nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("rewriting %s: %w", path, err)
	}
	return nil
}

// prLaneRecordList reads the issues this worktree recorded, in the order they were added.
func prLaneRecordList(deps prLaneDeps) []string {
	data, err := os.ReadFile(prLaneRecordPath(deps))
	if err != nil {
		return nil
	}
	var issues []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			issues = append(issues, line)
		}
	}
	return issues
}

// prLaneRecorded reports whether this worktree recorded an issue.
func prLaneRecorded(deps prLaneDeps, issue string) bool {
	for _, recorded := range prLaneRecordList(deps) {
		if recorded == issue {
			return true
		}
	}
	return false
}

// prLaneRefs lists every lane on the remote as `ref sha` lines. The remote is the authority:
// a lane claimed by another clone, machine or person is visible here, which no local file
// could do.
func prLaneRefs(deps prLaneDeps, repo string) []string {
	table := policy.PRLane()
	out, err := deps.gh("api", fmt.Sprintf("repos/%s/git/matching-refs/%s", repo, table.Namespace),
		"--jq", `.[] | "\(.ref) \(.object.sha)"`)
	if err != nil {
		return nil
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			refs = append(refs, strings.Join(fields, " "))
		}
	}
	return refs
}

// prLaneRecord is one lane's claim as the remote reports it.
type prLaneRecord struct {
	// Present is false when the ref does not exist.
	Present bool
	// Readable is false when the ref exists but its record cannot be read.
	Readable bool
	// Object is the ref's object: the claim record's own address.
	Object string
	// Message is the record's text: who claimed the lane.
	Message string
	// Claimed is when the claim was written.
	Claimed time.Time
	// ClaimedKnown is false when that moment could not be read.
	ClaimedKnown bool
}

// prLaneRead reads one lane's claim: the ref, then the tag object it points at, because the
// ref itself carries no timestamp.
func prLaneRead(deps prLaneDeps, repo, issue string) prLaneRecord {
	table := policy.PRLane()
	sha, err := deps.gh("api", fmt.Sprintf("repos/%s/git/ref/%s/%s", repo, table.Namespace, issue), "--jq", ".object.sha")
	if err != nil || strings.TrimSpace(sha) == "" {
		return prLaneRecord{}
	}
	record := prLaneRecord{Present: true, Object: strings.TrimSpace(sha)}
	out, err := deps.gh("api", fmt.Sprintf("repos/%s/git/tags/%s", repo, record.Object),
		"--jq", ".message, .tagger.date, .object.sha")
	if err != nil {
		return record
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return record
	}
	record.Message = strings.TrimSpace(lines[0])
	record.Readable = record.Message != ""
	if record.Readable {
		if at, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(lines[1])); parseErr == nil {
			record.Claimed = at
			record.ClaimedKnown = true
		}
	}
	return record
}

// prLaneOpenPR finds the open pull request that names an issue. A pull request names its
// issue through its head branch, whose name carries the issue number.
func prLaneOpenPR(deps prLaneDeps, repo, issue string) *claim.PullRequest {
	table := policy.PRLane()
	out, err := deps.gh("pr", "list", "--repo", repo, "--state", "open",
		"--limit", fmt.Sprint(table.PullRequestLimit), "--json", "number,headRefName",
		"--jq", `.[] | "\(.number) \(.headRefName)"`)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		number := 0
		if _, err := fmt.Sscanf(fields[0], "%d", &number); err != nil {
			continue
		}
		if claim.BranchNamesIssue(fields[1], issue) {
			return &claim.PullRequest{Number: number, Branch: fields[1]}
		}
	}
	return nil
}

// prLaneVerdict decides a lane: what is on the remote, whether a pull request holds it, and
// how long ago it was claimed. `check` and `claim` share it, so they cannot disagree.
func prLaneVerdict(deps prLaneDeps, repo, issue string) (claim.Verdict, prLaneRecord) {
	record := prLaneRead(deps, repo, issue)
	in := claim.LaneInput{
		Present:     record.Present,
		Readable:    record.Readable,
		Claimer:     record.Message,
		Claimed:     record.Claimed,
		AgeKnown:    record.ClaimedKnown,
		PullRequest: prLaneOpenPR(deps, repo, issue),
	}
	return claim.DecideLane(in, prLaneWindow(policy.PRLane()), deps.now()), record
}

// prLaneOpenIssue refuses a lane for an issue that is not open: a lane lands an open issue's
// pull request.
func prLaneOpenIssue(deps prLaneDeps, repo, issue string) error {
	out, err := deps.gh("issue", "view", issue, "--repo", repo, "--json", "state", "--jq", ".state")
	if err != nil {
		return fmt.Errorf("cannot read issue #%s (does it exist?)", issue)
	}
	if state := strings.TrimSpace(out); state != "OPEN" {
		if state == "" {
			state = "unknown"
		}
		return fmt.Errorf("issue #%s is %s — a lane lands an open issue's pull request", issue, state)
	}
	return nil
}

// prLaneClaimObject records the claim on the remote: an annotated tag object naming who
// claimed the lane, because the ref carries no timestamp and the pull request does not exist
// yet.
func prLaneClaimObject(deps prLaneDeps, repo, issue, base string) (string, error) {
	table := policy.PRLane()
	message := claim.ClaimMessage(prLaneBranch(deps), prLaneWorktree(deps))
	out, err := deps.gh("api", "-X", "POST", fmt.Sprintf("repos/%s/git/tags", repo),
		"-f", "tag="+table.Namespace+"-"+issue, "-f", "message="+message, "-f", "object="+base, "-f", "type=commit",
		"--jq", ".sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// prLaneTryClaim is the compare-and-swap: creating the ref succeeds exactly once, so of any
// number of simultaneous claimers exactly one wins whatever order they arrive in. The three
// outcomes are the protocol's: won, the lane exists, or something else went wrong.
func prLaneTryClaim(deps prLaneDeps, repo, issue, tag string) (bool, error) {
	table := policy.PRLane()
	_, err := deps.gh("api", "-X", "POST", fmt.Sprintf("repos/%s/git/refs", repo),
		"-f", "ref="+table.RefPrefix+issue, "-f", "sha="+tag, "--jq", ".ref")
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return false, nil
	}
	return false, fmt.Errorf("claiming the lane for #%s failed: %v", issue, err)
}

// prLaneDropRef deletes a lane's ref. It answers whether the ref was there: a lane that is
// already gone is not an error, it is a state.
func prLaneDropRef(deps prLaneDeps, repo, issue string) (bool, error) {
	table := policy.PRLane()
	_, err := deps.gh("api", "-X", "DELETE", fmt.Sprintf("repos/%s/git/refs/%s/%s", repo, table.Namespace, issue))
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "does not exist") || strings.Contains(message, "not found") {
		return false, nil
	}
	return false, fmt.Errorf("releasing the lane for #%s failed: %v", issue, err)
}

// prLaneClaim takes the lane, or takes over a claim past its window.
func prLaneClaim(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	issue, err := prLaneIssue(first(args))
	if err != nil {
		return err
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}
	if err := prLaneOpenIssue(deps, repo, issue); err != nil {
		return err
	}
	base, err := prLaneBase(deps)
	if err != nil {
		return err
	}
	window := int(prLaneWindow(policy.PRLane()).Minutes())

	for attempt := 1; attempt <= 3; attempt++ {
		tag, err := prLaneClaimObject(deps, repo, issue, base)
		if err != nil {
			return fmt.Errorf("cannot record the claim for #%s: %v", issue, err)
		}
		if tag == "" {
			return fmt.Errorf("cannot record the claim for #%s (empty tag object)", issue)
		}
		won, err := prLaneTryClaim(deps, repo, issue, tag)
		if err != nil {
			return err
		}
		if won {
			if err := prLaneRecordAdd(deps, issue); err != nil {
				return err
			}
			short := base
			if len(short) > 12 {
				short = short[:12]
			}
			fmt.Fprintf(out, "pr-lane: claimed #%s — %s%s at %s\n", issue, policy.PRLane().RefPrefix, issue, short)
			fmt.Fprintln(out, "pr-lane: push the branch and open the pull request as a draft now; the PR is the lease")
			return nil
		}

		verdict, _ := prLaneVerdict(deps, repo, issue)
		switch verdict.State {
		case claim.Held:
			return fmt.Errorf("lane #%s is held by pull request #%d (%s) — wait for it to merge, or close it if the work is abandoned; a closed PR releases the lane",
				issue, verdict.PullRequest.Number, verdict.PullRequest.Branch)
		case claim.Claiming:
			return fmt.Errorf("lane #%s was claimed %s minutes ago by %s and has no pull request yet — it is inside the %d-minute claim window; wait, or release it if that session is gone",
				issue, ageText(verdict, "some"), claimerText(verdict), window)
		case claim.Unreadable:
			return fmt.Errorf("lane #%s exists but its claim record cannot be read — release it deliberately with 'go run ./cmd/buildtool pr-lane release %s' if it is abandoned", issue, issue)
		}
		if attempt == 1 {
			fmt.Fprintf(errOut, "pr-lane: lane #%s is stale (%s, %s minutes old, no pull request) — taking it over\n",
				issue, claimerText(verdict), ageText(verdict, "?"))
		}
		if _, err := prLaneDropRef(deps, repo, issue); err != nil {
			return err
		}
	}
	return fmt.Errorf("another claimer keeps winning the race for #%s; retry", issue)
}

// prLaneCheck reports whether a claim would succeed, and claims nothing.
func prLaneCheck(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	issue, err := prLaneIssue(first(args))
	if err != nil {
		return err
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}
	if err := prLaneOpenIssue(deps, repo, issue); err != nil {
		return err
	}
	verdict, _ := prLaneVerdict(deps, repo, issue)
	window := int(prLaneWindow(policy.PRLane()).Minutes())
	switch verdict.State {
	case claim.Free:
		fmt.Fprintf(out, "pr-lane: lane #%s is free\n", issue)
		return nil
	case claim.Held:
		return fmt.Errorf("lane #%s is held by pull request #%d (%s)", issue, verdict.PullRequest.Number, verdict.PullRequest.Branch)
	case claim.Claiming:
		return fmt.Errorf("lane #%s was claimed %s minutes ago by %s; no pull request yet, so it is inside the %d-minute claim window",
			issue, ageText(verdict, "some"), claimerText(verdict), window)
	case claim.Stale:
		fmt.Fprintf(out, "pr-lane: lane #%s is stale (%s, %s minutes old, no pull request) — a claim would take it over\n",
			issue, claimerText(verdict), ageText(verdict, "?"))
		return nil
	default:
		return fmt.Errorf("lane #%s exists but its claim record cannot be read", issue)
	}
}

// prLaneStatus lists the lanes on the remote and the pull request behind each one.
func prLaneStatus(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	want := ""
	asJSON := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "" || strings.ContainsFunc(arg, func(r rune) bool { return r < '0' || r > '9' }):
			return usageError{fmt.Sprintf("unknown argument %q (status takes an optional issue number and --json)", arg)}
		default:
			want = arg
		}
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}

	var statuses []claim.Status
	for _, ref := range prLaneRefs(deps, repo) {
		fields := strings.Fields(ref)
		if len(fields) != 2 {
			continue
		}
		issue, ok := claim.IssueOf(fields[0], policy.PRLane().RefPrefix)
		if !ok || (want != "" && want != issue) {
			continue
		}
		verdict, record := prLaneVerdict(deps, repo, issue)
		status := claim.Status{
			Issue:  issue,
			Ref:    fields[0],
			Object: fields[1],
			Mine:   prLaneRecorded(deps, issue),
			State:  verdict.State.String(),
		}
		if verdict.PullRequest != nil {
			number := verdict.PullRequest.Number
			status.PullRequest = &number
			status.Branch = verdict.PullRequest.Branch
		}
		if verdict.State == claim.Unreadable {
			status.Claimant = "unreadable claim record"
		} else if record.Present {
			status.Claimant = verdict.Claimer
		}
		if verdict.AgeKnown {
			minutes := int(verdict.Age.Minutes())
			status.AgeMinutes = &minutes
		}
		statuses = append(statuses, status)
	}

	if asJSON {
		rendered, err := claim.StatusJSON(statuses)
		if err != nil {
			return err
		}
		fmt.Fprint(out, rendered)
		return nil
	}
	for _, status := range statuses {
		mine := ""
		if status.Mine {
			mine = " — this worktree"
		}
		switch status.State {
		case claim.Held.String():
			fmt.Fprintf(out, "#%s  %s  pull request #%d (%s)%s\n", status.Issue, status.Ref, *status.PullRequest, status.Branch, mine)
		case claim.Claiming.String():
			fmt.Fprintf(out, "#%s  %s  claimed %s minutes ago by %s, no pull request yet%s\n",
				status.Issue, status.Ref, ageOf(status), status.Claimant, mine)
		default:
			fmt.Fprintf(out, "#%s  %s  stale: %s (%s minutes old)%s\n",
				status.Issue, status.Ref, status.Claimant, ageOf(status), mine)
		}
	}
	return nil
}

// prLaneRelease frees the lane, deliberately: an open pull request keeps it unless the
// caller says otherwise.
func prLaneRelease(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	issue, err := prLaneIssue(first(args))
	if err != nil {
		return err
	}
	force := false
	if len(args) > 1 {
		if args[1] != "--force" {
			return usageError{fmt.Sprintf("unknown argument %q (release takes an issue number and an optional --force)", args[1])}
		}
		force = true
	}
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}
	if pr := prLaneOpenPR(deps, repo, issue); pr != nil && !force {
		return fmt.Errorf("pull request #%d (%s) is still open for #%s — merge or close it first, or pass --force to free the lane anyway",
			pr.Number, pr.Branch, issue)
	}
	dropped, err := prLaneDropRef(deps, repo, issue)
	if err != nil {
		return err
	}
	if err := prLaneRecordRemove(deps, issue); err != nil {
		return err
	}
	if !dropped {
		fmt.Fprintf(out, "pr-lane: lane #%s was already free\n", issue)
		return nil
	}
	fmt.Fprintf(out, "pr-lane: released #%s — %s%s deleted\n", issue, policy.PRLane().RefPrefix, issue)
	return nil
}

// prLaneWhoami reports this worktree and the lanes it recorded locally, so a session tells
// its own landing from a stranger's without a network round trip.
func prLaneWhoami(args []string, out, errOut io.Writer, deps prLaneDeps) error {
	repo, err := prLaneRepo(deps)
	if err != nil {
		return err
	}
	recorded := prLaneRecordList(deps)
	sort.Strings(recorded)
	mine := "none"
	if len(recorded) > 0 {
		mine = strings.Join(recorded, " ")
	}
	fmt.Fprintf(out, "pr-lane: %s @ %s (%s); claimed here: %s\n", repo, prLaneBranch(deps), prLaneWorktree(deps), mine)
	return nil
}

// ageText renders a verdict's age in whole minutes, or the caller's word for an age the
// record did not carry.
func ageText(verdict claim.Verdict, unknown string) string {
	if !verdict.AgeKnown {
		return unknown
	}
	return fmt.Sprint(int(verdict.Age.Minutes()))
}

// ageOf renders a status row's age, which is "?" when the record carried none.
func ageOf(status claim.Status) string {
	if status.AgeMinutes == nil {
		return "?"
	}
	return fmt.Sprint(*status.AgeMinutes)
}

// claimerText renders who the record says claimed the lane.
func claimerText(verdict claim.Verdict) string {
	if verdict.Claimer == "" {
		return "another session"
	}
	return verdict.Claimer
}

// first returns the first argument, or "".
func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
