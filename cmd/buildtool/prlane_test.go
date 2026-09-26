package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/build/core/claim"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// laneTag is a claim record as the remote stores it: an annotated tag object carrying the
// message and the moment, which is what makes the lane readable from any clone.
type laneTag struct {
	message string
	date    time.Time
}

// fakeLane is a stub remote. It is the in-process equivalent of the shell harness's stub
// `gh`: the ref create is atomic, so the claim protocol can be exercised without a network
// and without a second clone.
type fakeLane struct {
	mu sync.Mutex
	// refs maps an issue to the tag object its ref points at.
	refs map[string]string
	// tags maps a tag object's sha to its record.
	tags map[string]laneTag
	// issues maps an issue to its state, defaulting to OPEN.
	issues map[string]string
	// pulls are the open pull requests, by number and head branch.
	pulls []claim.PullRequest
	// repo is what `repo view` answers.
	repo string
	// tagSeq numbers created tag objects so each has a distinct sha.
	tagSeq int
}

func newFakeLane() *fakeLane {
	return &fakeLane{
		refs:   map[string]string{},
		tags:   map[string]laneTag{},
		issues: map[string]string{},
		repo:   "owner/name",
	}
}

// seedClaim records a claim as a remote would hold it.
func (f *fakeLane) seedClaim(issue, message string, claimed time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagSeq++
	sha := fmt.Sprintf("tag%04d", f.tagSeq)
	f.tags[sha] = laneTag{message: message, date: claimed}
	f.refs[issue] = sha
}

// seedUnreadableRef records a ref whose tag object cannot be read.
func (f *fakeLane) seedUnreadableRef(issue string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[issue] = "dangling"
}

// gh answers the calls the command makes, in the shapes it makes them.
func (f *fakeLane) gh(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	joined := strings.Join(args, " ")
	switch {
	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/matching-refs/"):
		var lines []string
		for issue, sha := range f.refs {
			lines = append(lines, fmt.Sprintf("%s%s %s", policy.PRLane().RefPrefix, issue, sha))
		}
		return strings.Join(lines, "\n"), nil

	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/ref/"):
		issue := lastSegment(apiPath(args))
		sha, found := f.refs[issue]
		if !found {
			return "", errors.New("Not Found")
		}
		return sha + "\n", nil

	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/tags/") && !strings.Contains(joined, "-X POST"):
		sha := lastSegment(apiPath(args))
		tag, found := f.tags[sha]
		if !found {
			return "", errors.New("Not Found")
		}
		return fmt.Sprintf("%s\n%s\n%s\n", tag.message, tag.date.UTC().Format(time.RFC3339), "base"), nil

	case strings.HasPrefix(joined, "api -X POST") && strings.Contains(joined, "/git/tags"):
		f.tagSeq++
		sha := fmt.Sprintf("tag%04d", f.tagSeq)
		f.tags[sha] = laneTag{message: field(args, "message="), date: time.Now().UTC()}
		return sha + "\n", nil

	case strings.HasPrefix(joined, "api -X POST") && strings.Contains(joined, "/git/refs"):
		issue := strings.TrimPrefix(field(args, "ref="), policy.PRLane().RefPrefix)
		if _, exists := f.refs[issue]; exists {
			return "", errors.New("Reference already exists")
		}
		f.refs[issue] = field(args, "sha=")
		return policy.PRLane().RefPrefix + issue + "\n", nil

	case strings.HasPrefix(joined, "api -X DELETE") && strings.Contains(joined, "/git/refs/"):
		issue := lastSegment(apiPath(args))
		if _, exists := f.refs[issue]; !exists {
			return "", errors.New("Reference does not exist")
		}
		delete(f.refs, issue)
		return "", nil

	case strings.HasPrefix(joined, "pr list"):
		var lines []string
		for _, pull := range f.pulls {
			lines = append(lines, fmt.Sprintf("%d %s", pull.Number, pull.Branch))
		}
		return strings.Join(lines, "\n"), nil

	case strings.HasPrefix(joined, "issue view"):
		state, found := f.issues[numericArg(args)]
		if !found {
			state = "OPEN"
		}
		return state + "\n", nil

	case strings.HasPrefix(joined, "repo view"):
		return f.repo + "\n", nil
	}
	return "", fmt.Errorf("unexpected gh call: %s", joined)
}

// git answers the queries the command makes.
func (f *fakeLane) git(gitDir string) func(args ...string) (string, error) {
	return func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "config --get remote.origin.url":
			return "https://github.com/owner/name.git\n", nil
		case strings.Contains(joined, "--git-dir"):
			return gitDir + "\n", nil
		case joined == "rev-parse --abbrev-ref HEAD":
			return "chore/lane/389\n", nil
		case joined == "rev-parse --show-toplevel":
			return "/src/wt-389\n", nil
		case strings.HasPrefix(joined, "ls-remote"):
			return "0123456789abcdef0123456789abcdef01234567\trefs/heads/main\n", nil
		case joined == "rev-parse origin/main":
			return "0123456789abcdef0123456789abcdef01234567\n", nil
		}
		return "", fmt.Errorf("unexpected git call: %s", joined)
	}
}

// field returns the value of a `-f name=value` argument.
func field(args []string, prefix string) string {
	for _, arg := range args {
		if value, found := strings.CutPrefix(arg, prefix); found {
			return value
		}
	}
	return ""
}

// lastSegment returns the last path segment of the API path argument.
func lastSegment(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	return trimmed[strings.LastIndex(trimmed, "/")+1:]
}

// apiPath returns the argument naming the API resource, which is the `repos/...` one. It is
// found rather than indexed because a mutating call puts the method first (`api -X DELETE
// repos/...`), so the path is not always in the same position.
func apiPath(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "repos/") {
			return arg
		}
	}
	return ""
}

// numericArg returns the first all-digit argument, which is how an issue number reaches the
// CLI (`issue view <n> --repo ...`).
func numericArg(args []string) string {
	for _, arg := range args {
		if arg == "" || strings.ContainsFunc(arg, func(r rune) bool { return r < '0' || r > '9' }) {
			continue
		}
		return arg
	}
	return ""
}

// laneDeps builds the collaborators for a fake remote and a git dir.
func laneDeps(f *fakeLane, gitDir string) prLaneDeps {
	return prLaneDeps{gh: f.gh, git: f.git(gitDir), now: func() time.Time { return time.Now().UTC() }}
}

// runPRLaneCommand drives one invocation and returns its output and exit status.
func runPRLaneCommand(t *testing.T, deps prLaneDeps, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := prLane(args, &out, &errOut, deps)
	switch {
	case err == nil:
		status = 0
	default:
		var statusErr statusError
		var usage usageError
		switch {
		case errors.As(err, &statusErr):
			status = statusErr.code
			if statusErr.message != "" {
				errOut.WriteString(statusErr.message + "\n")
			}
		case errors.As(err, &usage):
			status = 2
			errOut.WriteString(usage.message + "\n")
		default:
			status = 1
			errOut.WriteString(err.Error() + "\n")
		}
	}
	return out.String(), errOut.String(), status
}

// TestPRLaneClaimTakesAFreeLane pins the claim: the ref is created, the local record notes
// it, and the caller is told to open the pull request because the PR is the lease.
func TestPRLaneClaimTakesAFreeLane(t *testing.T) {
	fake := newFakeLane()
	deps := laneDeps(fake, t.TempDir())

	out, errOut, status := runPRLaneCommand(t, deps, "claim", "389")
	if status != 0 {
		t.Fatalf("claim = %d, want 0\n%s", status, errOut)
	}
	if !strings.Contains(out, "pr-lane: claimed #389 — refs/lane/389 at 0123456789ab") {
		t.Errorf("claim reported %q, want the ref and the base's short sha", out)
	}
	if !strings.Contains(out, "the PR is the lease") {
		t.Errorf("claim did not say the pull request is the lease: %q", out)
	}
	if fake.refs["389"] == "" {
		t.Error("the claim did not create the ref")
	}
	if recorded := prLaneRecordList(deps); len(recorded) != 1 || recorded[0] != "389" {
		t.Errorf("the local record is %q, want the claimed issue", recorded)
	}
	// The ref points at a claim record naming who claimed the lane: the ref itself
	// carries no timestamp and there is no pull request yet, so that record is the only
	// way another session reads the claim.
	record, found := fake.tags[fake.refs["389"]]
	if !found {
		t.Fatal("the ref does not point at a claim record")
	}
	if !strings.Contains(record.message, "branch=chore/lane/389") || !strings.Contains(record.message, "worktree=wt-389") {
		t.Errorf("the claim record is %q, want the branch and the worktree", record.message)
	}
}

// TestPRLaneRefusesAClosedIssue pins the first gate: a lane lands an open issue's pull
// request, so a closed issue is refused before any claim is attempted.
func TestPRLaneRefusesAClosedIssue(t *testing.T) {
	fake := newFakeLane()
	fake.issues["391"] = "CLOSED"
	deps := laneDeps(fake, t.TempDir())

	out, errOut, status := runPRLaneCommand(t, deps, "claim", "391")
	if status != 1 {
		t.Fatalf("claim of a closed issue = %d, want 1\n%s%s", status, out, errOut)
	}
	if !strings.Contains(errOut, "issue #391 is CLOSED") {
		t.Errorf("the refusal %q does not name the issue state", errOut)
	}
	if len(fake.refs) != 0 {
		t.Error("the refused claim created a ref")
	}

	if _, errOut, status := runPRLaneCommand(t, deps, "check", "391"); status != 1 || !strings.Contains(errOut, "CLOSED") {
		t.Errorf("check of a closed issue = %d %q, want the same refusal", status, errOut)
	}
}

// TestPRLaneClaimRefusesWhileAPullRequestIsOpen pins the lease: an open pull request naming
// the issue holds the lane, whatever the claim's age.
func TestPRLaneClaimRefusesWhileAPullRequestIsOpen(t *testing.T) {
	fake := newFakeLane()
	fake.pulls = []claim.PullRequest{{Number: 42, Branch: "chore/lane/389-x"}}
	fake.seedClaim("389", "branch=x worktree=y", time.Now().UTC().Add(-500*time.Minute))
	deps := laneDeps(fake, t.TempDir())

	_, errOut, status := runPRLaneCommand(t, deps, "claim", "389")
	if status != 1 {
		t.Fatalf("claim = %d, want 1", status)
	}
	if !strings.Contains(errOut, "held by pull request #42 (chore/lane/389-x)") {
		t.Errorf("the refusal %q does not name the pull request", errOut)
	}
	if !strings.Contains(errOut, "a closed PR releases the lane") {
		t.Errorf("the refusal %q does not say how the lane is freed", errOut)
	}
}

// TestPRLaneClaimRefusesInsideTheWindow pins the window: a claim with no pull request yet is
// honoured, so the next claimer waits rather than taking a live landing away.
func TestPRLaneClaimRefusesInsideTheWindow(t *testing.T) {
	fake := newFakeLane()
	fake.seedClaim("389", "branch=x worktree=wt-1", time.Now().UTC().Add(-10*time.Minute))
	deps := laneDeps(fake, t.TempDir())

	_, errOut, status := runPRLaneCommand(t, deps, "claim", "389")
	if status != 1 {
		t.Fatalf("claim = %d, want 1", status)
	}
	for _, want := range []string{"claimed 10 minutes ago", "branch=x worktree=wt-1", "90-minute claim window"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal %q does not carry %q", errOut, want)
		}
	}
}

// TestPRLaneClaimTakesOverAStaleClaim pins the takeover: past the window the next claimer
// proceeds, and says so, so nobody has to judge whether the holder is dead.
func TestPRLaneClaimTakesOverAStaleClaim(t *testing.T) {
	fake := newFakeLane()
	fake.seedClaim("389", "branch=x worktree=wt-1", time.Now().UTC().Add(-200*time.Minute))
	before := fake.refs["389"]
	deps := laneDeps(fake, t.TempDir())

	out, errOut, status := runPRLaneCommand(t, deps, "claim", "389")
	if status != 0 {
		t.Fatalf("claim = %d, want 0\n%s", status, errOut)
	}
	if !strings.Contains(errOut, "is stale (branch=x worktree=wt-1, 200 minutes old, no pull request) — taking it over") {
		t.Errorf("the takeover was not reported: %q", errOut)
	}
	if !strings.Contains(out, "claimed #389") {
		t.Errorf("the takeover did not report the claim: %q", out)
	}
	if fake.refs["389"] == before {
		t.Error("the takeover left the old claim in place")
	}
}

// TestPRLaneClaimRefusesAnUnreadableRecord pins the one state nothing can be decided from:
// a claim whose record cannot be read is cleared deliberately, not stolen.
func TestPRLaneClaimRefusesAnUnreadableRecord(t *testing.T) {
	fake := newFakeLane()
	fake.seedUnreadableRef("389")
	deps := laneDeps(fake, t.TempDir())

	_, errOut, status := runPRLaneCommand(t, deps, "claim", "389")
	if status != 1 {
		t.Fatalf("claim = %d, want 1", status)
	}
	if !strings.Contains(errOut, "claim record cannot be read") || !strings.Contains(errOut, "release 389") {
		t.Errorf("the refusal %q does not name the state and the remedy", errOut)
	}
}

// TestPRLaneCheck pins the read-only verb: it reports whether a claim would succeed and
// claims nothing.
func TestPRLaneCheck(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(*fakeLane)
		wantStatus int
		wantOut    string
		wantErr    string
	}{
		{
			name:       "free",
			wantStatus: 0,
			wantOut:    "pr-lane: lane #389 is free",
		},
		{
			// A held lane is one somebody claimed; the pull request, not the claim's age,
			// is what holds it. The ref must be there: with no ref at all the lane is
			// free, whatever pull requests name the issue.
			name: "held",
			prepare: func(f *fakeLane) {
				f.pulls = []claim.PullRequest{{Number: 42, Branch: "chore/lane/389-x"}}
				f.seedClaim("389", "branch=x", time.Now().UTC().Add(-5*time.Minute))
			},
			wantStatus: 1,
			wantErr:    "held by pull request #42",
		},
		{
			name:       "inside the window",
			prepare:    func(f *fakeLane) { f.seedClaim("389", "branch=x", time.Now().UTC().Add(-5*time.Minute)) },
			wantStatus: 1,
			wantErr:    "inside the 90-minute claim window",
		},
		{
			name:       "stale",
			prepare:    func(f *fakeLane) { f.seedClaim("389", "branch=x", time.Now().UTC().Add(-200*time.Minute)) },
			wantStatus: 0,
			wantOut:    "a claim would take it over",
		},
		{
			name:       "unreadable",
			prepare:    func(f *fakeLane) { f.seedUnreadableRef("389") },
			wantStatus: 1,
			wantErr:    "claim record cannot be read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeLane()
			if tc.prepare != nil {
				tc.prepare(fake)
			}
			out, errOut, status := runPRLaneCommand(t, laneDeps(fake, t.TempDir()), "check", "389")
			if status != tc.wantStatus {
				t.Fatalf("check = %d, want %d\n%s", status, tc.wantStatus, errOut)
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Errorf("check reported %q, want %q", out, tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("check refused with %q, want %q", errOut, tc.wantErr)
			}
			if len(fake.refs) != 0 && tc.name == "free" {
				t.Error("check created a ref")
			}
		})
	}
}

// TestPRLaneStatus pins both forms: the lines a reader reads, and the JSON shape a tool
// reads — including the nulls for a missing pull request and an unknown age.
func TestPRLaneStatus(t *testing.T) {
	fake := newFakeLane()
	fake.pulls = []claim.PullRequest{{Number: 42, Branch: "chore/lane/389-x"}}
	fake.seedClaim("389", "branch=chore/lane/389 worktree=wt-389", time.Now().UTC().Add(-5*time.Minute))
	fake.seedClaim("390", "branch=chore/lane/390 worktree=wt-390", time.Now().UTC().Add(-200*time.Minute))
	fake.seedUnreadableRef("391")
	deps := laneDeps(fake, t.TempDir())
	if err := prLaneRecordAdd(deps, "389"); err != nil {
		t.Fatalf("recording the claim: %v", err)
	}

	out, errOut, status := runPRLaneCommand(t, deps, "status")
	if status != 0 {
		t.Fatalf("status = %d\n%s", status, errOut)
	}
	for _, want := range []string{
		"#389  refs/lane/389  pull request #42 (chore/lane/389-x) — this worktree",
		"#390  refs/lane/390  stale:",
		"#391  refs/lane/391  stale: unreadable claim record",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status printed %q, want a line containing %q", out, want)
		}
	}

	jsonOut, errOut, status := runPRLaneCommand(t, deps, "status", "--json")
	if status != 0 {
		t.Fatalf("status --json = %d\n%s", status, errOut)
	}
	var statuses []claim.Status
	if err := json.Unmarshal([]byte(jsonOut), &statuses); err != nil {
		t.Fatalf("status --json printed unparseable JSON: %v\n%s", err, jsonOut)
	}
	if len(statuses) != 3 {
		t.Fatalf("status --json listed %d lanes, want 3", len(statuses))
	}
	byIssue := map[string]claim.Status{}
	for _, s := range statuses {
		byIssue[s.Issue] = s
	}
	held := byIssue["389"]
	if held.State != "held" || held.PullRequest == nil || *held.PullRequest != 42 || !held.Mine || held.Branch != "chore/lane/389-x" {
		t.Errorf("the held lane rendered as %+v", held)
	}
	if held.AgeMinutes != nil {
		t.Errorf("a held lane reports an age: %+v", held.AgeMinutes)
	}
	stale := byIssue["390"]
	if stale.State != "stale" || stale.Mine || stale.AgeMinutes == nil || *stale.AgeMinutes < 190 {
		t.Errorf("the stale lane rendered as %+v", stale)
	}
	unreadable := byIssue["391"]
	if unreadable.State != "unreadable" || unreadable.Claimant != "unreadable claim record" {
		t.Errorf("the unreadable lane rendered as %+v", unreadable)
	}

	// A single issue narrows the listing to it.
	out, _, status = runPRLaneCommand(t, deps, "status", "390")
	if status != 0 || strings.Contains(out, "389") || !strings.Contains(out, "390") {
		t.Errorf("status 390 printed %q", out)
	}
}

// TestPRLaneRelease pins the deliberate exit: an open pull request keeps the lane unless the
// caller says otherwise, and releasing clears the local record too.
func TestPRLaneRelease(t *testing.T) {
	fake := newFakeLane()
	fake.pulls = []claim.PullRequest{{Number: 42, Branch: "chore/lane/389-x"}}
	fake.seedClaim("389", "branch=x", time.Now().UTC())
	deps := laneDeps(fake, t.TempDir())
	if err := prLaneRecordAdd(deps, "389"); err != nil {
		t.Fatalf("recording the claim: %v", err)
	}

	_, errOut, status := runPRLaneCommand(t, deps, "release", "389")
	if status != 1 || !strings.Contains(errOut, "is still open for #389") {
		t.Fatalf("release with an open pull request = %d %q, want a refusal", status, errOut)
	}
	if fake.refs["389"] == "" {
		t.Fatal("the refused release deleted the ref")
	}

	out, errOut, status := runPRLaneCommand(t, deps, "release", "389", "--force")
	if status != 0 {
		t.Fatalf("release --force = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "released #389 — refs/lane/389 deleted") {
		t.Errorf("release reported %q", out)
	}
	if _, exists := fake.refs["389"]; exists {
		t.Error("release left the ref in place")
	}
	if recorded := prLaneRecordList(deps); len(recorded) != 0 {
		t.Errorf("release left the local record: %q", recorded)
	}

	// Releasing an already-free lane is not an error. The pull request is cleared first:
	// while it is open it legitimately keeps the lane, which is the refusal pinned above.
	fake.pulls = nil
	out, _, status = runPRLaneCommand(t, deps, "release", "389")
	if status != 0 || !strings.Contains(out, "was already free") {
		t.Errorf("releasing a free lane = %d %q, want 'already free'", status, out)
	}
}

// TestPRLaneWhoami pins the local answer: this worktree, and the lanes it recorded.
func TestPRLaneWhoami(t *testing.T) {
	fake := newFakeLane()
	deps := laneDeps(fake, t.TempDir())
	if err := prLaneRecordAdd(deps, "389"); err != nil {
		t.Fatalf("recording the claim: %v", err)
	}

	out, errOut, status := runPRLaneCommand(t, deps, "whoami")
	if status != 0 {
		t.Fatalf("whoami = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "pr-lane: owner/name @ chore/lane/389 (wt-389); claimed here: 389") {
		t.Errorf("whoami printed %q", out)
	}
}

// TestPRLaneClaimIsAtomic pins the protocol: of four simultaneous claimers exactly one wins,
// because the ref create is the compare-and-swap. A check followed by a write would let two
// sessions hold one lane.
func TestPRLaneClaimIsAtomic(t *testing.T) {
	fake := newFakeLane()
	deps := laneDeps(fake, t.TempDir())

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		loses int
	)
	for contender := 0; contender < 4; contender++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errOut bytes.Buffer
			err := prLane([]string{"claim", "389"}, &out, &errOut, deps)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				loses++
			}
		}()
	}
	wg.Wait()
	if wins != 1 || loses != 3 {
		t.Fatalf("%d winners and %d losers, want exactly one winner", wins, loses)
	}
}

// TestPRLaneRejectsBadInvocations pins the invocation contract: a missing or malformed issue
// is the caller's mistake, and the status says so.
func TestPRLaneRejectsBadInvocations(t *testing.T) {
	fake := newFakeLane()
	deps := laneDeps(fake, t.TempDir())

	for _, args := range [][]string{
		{},
		{"nope"},
		{"claim"},
		{"claim", "abc"},
		{"check", "38a"},
		{"release"},
		{"release", "389", "--nope"},
		{"status", "--nope"},
	} {
		out, errOut, status := runPRLaneCommand(t, deps, args...)
		if status != 2 {
			t.Errorf("pr-lane %q = %d %q, want the usage status", args, status, out+errOut)
		}
		if len(fake.refs) != 0 {
			t.Errorf("pr-lane %q changed the remote", args)
		}
	}
}

// TestPRLaneRepoResolution pins the two URL shapes git writes for an origin remote, and the
// override a caller with no remote uses.
func TestPRLaneRepoResolution(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{url: "https://github.com/owner/name.git", want: "owner/name"},
		{url: "https://github.com/owner/name", want: "owner/name"},
		{url: "git@github.com:owner/name.git", want: "owner/name"},
		{url: "ssh://git@github.com/owner/name.git", want: "owner/name"},
		{url: "not a url", want: ""},
	}
	for _, tc := range cases {
		if got := repoFromURL(tc.url); got != tc.want {
			t.Errorf("repoFromURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}

	// The override answers without reading a remote at all.
	t.Setenv(policy.PRLane().RepoEnv, "override/repo")
	deps := prLaneDeps{
		gh: func(args ...string) (string, error) { return "", errors.New("gh must not be called") },
		git: func(args ...string) (string, error) {
			return "", errors.New("git must not be called")
		},
		now: time.Now,
	}
	repo, err := prLaneRepo(deps)
	if err != nil || repo != "override/repo" {
		t.Errorf("prLaneRepo = %q, %v; want the override", repo, err)
	}
}

// TestPRLaneWindowOverride pins the window's override, which a caller uses to tighten or
// loosen the claim window for one invocation.
func TestPRLaneWindowOverride(t *testing.T) {
	table := policy.PRLane()
	if got := prLaneWindow(table); got != time.Duration(table.StaleMinutes)*time.Minute {
		t.Errorf("the window is %v, want the table's %d minutes", got, table.StaleMinutes)
	}
	t.Setenv(table.StaleEnv, "5")
	if got := prLaneWindow(table); got != 5*time.Minute {
		t.Errorf("the overridden window is %v, want 5m", got)
	}
	// A window set to zero makes every claim instantly stale, which a test above relies on.
	t.Setenv(table.StaleEnv, "0")
	if got := prLaneWindow(table); got != 0 {
		t.Errorf("the zero window is %v", got)
	}
}

// TestPRLaneRecord pins the local record: an issue is added once, removed without leaving an
// empty file, and read back in order.
func TestPRLaneRecord(t *testing.T) {
	deps := laneDeps(newFakeLane(), t.TempDir())

	for _, issue := range []string{"389", "390", "389"} {
		if err := prLaneRecordAdd(deps, issue); err != nil {
			t.Fatalf("recording %s: %v", issue, err)
		}
	}
	if recorded := prLaneRecordList(deps); strings.Join(recorded, ",") != "389,390" {
		t.Errorf("the record is %q, want each issue once", recorded)
	}
	if err := prLaneRecordRemove(deps, "389"); err != nil {
		t.Fatalf("removing 389: %v", err)
	}
	if recorded := prLaneRecordList(deps); strings.Join(recorded, ",") != "390" {
		t.Errorf("the record is %q, want only 390", recorded)
	}
	if err := prLaneRecordRemove(deps, "390"); err != nil {
		t.Fatalf("removing 390: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(prLaneRecordPath(deps)), policy.PRLane().RecordFile)); !os.IsNotExist(err) {
		t.Errorf("the record file survived its last issue: %v", err)
	}
}
