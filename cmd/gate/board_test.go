package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/build/board"
)

// fakeCoord is a stub remote and repository for the three coordinator verbs. It answers the
// calls the commands make, in the shapes they make them, so the verbs are exercised without a
// network and without a second clone — the same way prlane_test.go drives the lane protocol.
type fakeCoord struct {
	issues string
	pulls  string
	refs   string
	tags   map[string]string
	times  map[string]string
	// pullsByNumber answers `pr view <number>`.
	pullsByNumber map[string]string
	// issueViews answers `issue view <number>`.
	issueViews map[string]string
	// searched answers `pr list --search`.
	searched string
	// worktrees is the porcelain listing for the collisions verb.
	worktrees string
	// diffs maps a worktree path to the diff the collisions verb reads there.
	diffs map[string]string
	// showFiles is what `git show --name-only` prints.
	showFiles string
	// diffsByPath maps a path to the diff `git show --unified=0 ... -- <path>` prints.
	diffsByPath map[string]string
	// parents answers `git rev-list --parents -1 <sha>`.
	parents string
	// remotes maps a ref to the object `git ls-remote origin <ref>` prints.
	remotes map[string]string
	// signed maps a commit to whether `git verify-commit` accepts it.
	signed map[string]bool
	// headParents answers `git rev-list --parents HEAD` for the collisions verb.
	mergeBase string
	calls     []string
}

// newFakeCoord returns a coordinator stub with an armed-free, unclaimed world.
func newFakeCoord() *fakeCoord {
	return &fakeCoord{
		issues:        `[]`,
		pulls:         `[]`,
		tags:          map[string]string{},
		times:         map[string]string{},
		pullsByNumber: map[string]string{},
		issueViews:    map[string]string{},
		diffs:         map[string]string{},
		diffsByPath:   map[string]string{},
		remotes:       map[string]string{},
		signed:        map[string]bool{},
	}
}

// gh answers the board's, collisions' and verify-landing's `gh` calls.
func (f *fakeCoord) gh(args ...string) (string, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, "gh "+joined)
	switch {
	case strings.HasPrefix(joined, "repo view"):
		return "owner/name\n", nil
	case strings.HasPrefix(joined, "issue list"):
		return f.issues, nil
	case strings.HasPrefix(joined, "pr list") && strings.Contains(joined, "--search"):
		return f.searched, nil
	case strings.HasPrefix(joined, "pr list"):
		return f.pulls, nil
	case strings.HasPrefix(joined, "pr view"):
		number := numericArg(args)
		view, found := f.pullsByNumber[number]
		if !found {
			return "", fmt.Errorf("GraphQL: Could not resolve to a PullRequest with the number of %s", number)
		}
		return view, nil
	case strings.HasPrefix(joined, "issue view"):
		number := numericArg(args)
		view, found := f.issueViews[number]
		if !found {
			return "", fmt.Errorf("Could not resolve to an Issue with the number of %s", number)
		}
		return view, nil
	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/matching-refs/"):
		return f.refs, nil
	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/ref/"):
		issue := lastSegment(apiPath(args))
		sha, found := f.refsByIssue()[issue]
		if !found {
			return "", errors.New("Not Found")
		}
		return sha + "\n", nil
	case strings.HasPrefix(joined, "api repos/") && strings.Contains(joined, "/git/tags/"):
		sha := lastSegment(apiPath(args))
		message, found := f.tags[sha]
		if !found {
			return "", errors.New("Not Found")
		}
		at := f.times[sha]
		if at == "" {
			at = time.Now().UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s\n%s\n%s\n", message, at, "base"), nil
	}
	return "", fmt.Errorf("unexpected gh call: %s", joined)
}

// refsByIssue reads the matching-refs output as an issue-to-object map.
func (f *fakeCoord) refsByIssue() map[string]string {
	byIssue := map[string]string{}
	for _, line := range strings.Split(f.refs, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		byIssue[lastSegment(fields[0])] = fields[1]
	}
	return byIssue
}

// git answers the commands' `git` calls, in the working directory and in a worktree.
func (f *fakeCoord) git(args ...string) (string, error) { return f.gitIn("", args...) }

// gitIn answers one git call, letting the collisions verb's per-worktree query be driven by
// the `-C <dir>` the caller asked for, which is how the command runs a lane's own git.
func (f *fakeCoord) gitIn(dir string, args ...string) (string, error) {
	return f.gitRaw(args...)
}

// gitRaw answers a git call with its `-C <dir>` prefix removed, so one switch serves both the
// caller's directory and a lane's.
func (f *fakeCoord) gitRaw(args ...string) (string, error) {
	dir := ""
	if len(args) >= 2 && args[0] == "-C" {
		dir = args[1]
		args = args[2:]
	}
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, "git "+strings.Join(args, " "))
	switch {
	case joined == "config --get remote.origin.url":
		return "https://github.com/owner/name.git\n", nil
	case strings.HasPrefix(joined, "ls-remote"):
		if strings.Contains(joined, "refs/heads/main") {
			// The base ref, read the way `prLaneBase` asks for it.
			return "e25e7ef4e33a247bec43bcee844fda5ec8c2bdc6\trefs/heads/main\n", nil
		}
		ref := args[len(args)-1]
		if object, found := f.remotes[ref]; found {
			return object + "\t" + ref + "\n", nil
		}
		return "", nil
	case strings.HasPrefix(joined, "rev-parse --path-format=absolute --git-common-dir"):
		return "", errors.New("this stub keeps no clone")
	case joined == "rev-parse --abbrev-ref HEAD":
		return "chore/438-gate-board\n", nil
	case joined == "rev-parse --show-toplevel":
		return "/src/wt-438\n", nil
	case joined == "worktree list --porcelain":
		return f.worktrees, nil
	case strings.HasPrefix(joined, "rev-list --parents -1 "):
		if f.parents == "" {
			return "", errors.New("no such commit")
		}
		return f.parents, nil
	case strings.HasPrefix(joined, "rev-list --count "):
		return "3\n", nil
	case strings.HasPrefix(joined, "merge-base "):
		if f.mergeBase == "" {
			return "", errors.New("no merge base")
		}
		return f.mergeBase + "\n", nil
	case strings.HasPrefix(joined, "diff --unified=0 "):
		return f.diffs[dir], nil
	case strings.HasPrefix(joined, "show --name-only"):
		return f.showFiles, nil
	case strings.HasPrefix(joined, "show --unified=0"):
		path := args[len(args)-1]
		return f.diffsByPath[path], nil
	case strings.HasPrefix(joined, "verify-commit "):
		if f.signed[strings.TrimSpace(strings.TrimPrefix(joined, "verify-commit "))] {
			return "Good signature\n", nil
		}
		return "", errors.New("no signature")
	}
	return "", fmt.Errorf("unexpected git call: %s", joined)
}

// deps wires the stub into the verbs' collaborators.
func (f *fakeCoord) deps() prLaneDeps {
	return prLaneDeps{
		gh:  f.gh,
		git: f.git,
		now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}
}

// runCoordinatorVerb drives one invocation and reports its streams and exit status, the way
// `main` resolves them.
func runCoordinatorVerb(t *testing.T, verb func([]string, io.Writer, io.Writer, prLaneDeps) error, deps prLaneDeps, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := verb(args, &out, &errOut, deps)
	status := 0
	if err != nil {
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

// seedBoard gives the stub the shape the board test reads: two open issues, one claimed lane
// with an armed pull request behind it, and one issue nobody touched.
func seedBoard(f *fakeCoord) {
	f.issues = `[
	  {"number": 438, "title": "the board verbs", "url": "https://example.invalid/438", "labels": [{"name": "chore"}]},
	  {"number": 440, "title": "collisions", "url": "https://example.invalid/440", "labels": []}
	]`
	f.pulls = `[
	  {"number": 470, "title": "the board", "headRefName": "chore/438-gate-board",
	   "headRefOid": "aaaa1111bbbb2222cccc3333dddd4444eeee5555", "isDraft": false,
	   "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
	   "autoMergeRequest": {"enabledAt": "2026-09-28T09:00:00Z"}}
	]`
	f.refs = "refs/lane/438 tag0001\n"
	f.tags["tag0001"] = "branch=chore/438-gate-board worktree=wt-438"
	f.times["tag0001"] = "2026-09-28T11:30:00Z"
	f.worktrees = `worktree D:/src/go-cask
HEAD e25e7ef4aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
branch refs/heads/main

worktree D:/src/go-cask/.worktrees/wt-438
HEAD aaaa1111bbbb2222cccc3333dddd4444eeee5555
branch refs/heads/chore/438-gate-board
`
}

// TestBoardReadsEveryAuthority pins the board's join: the issue list is the spine, the lane
// ref names the claim, the worktree list says where the work is, the pull request is the
// lease, and the columns carry the draft and auto-merge fields §2 requires.
func TestBoardReadsEveryAuthority(t *testing.T) {
	f := newFakeCoord()
	seedBoard(f)

	out, errOut, status := runCoordinatorVerb(t, boardCommand, f.deps())
	if status != 0 {
		t.Fatalf("board = %d, want 0\n%s%s", status, out, errOut)
	}
	for _, want := range []string{
		"board v1 — 2 open issue(s), claim window 90 minutes",
		"#438", "438 (30m)", "wt-438", "#470 CLEAN armed", "fresh", "an armed pull request lands itself",
		"#440", "unclaimed", "choose: the issue is open and no lane, worktree or pull request names it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("board output does not contain %q:\n%s", want, out)
		}
	}
}

// TestBoardJSON pins the machine-readable form a coordinator's script reads.
func TestBoardJSON(t *testing.T) {
	f := newFakeCoord()
	seedBoard(f)

	out, errOut, status := runCoordinatorVerb(t, boardCommand, f.deps(), "--json")
	if status != 0 {
		t.Fatalf("board --json = %d, want 0\n%s", status, errOut)
	}
	for _, want := range []string{`"version": 1`, `"holder": "wt-438"`, `"claim": "fresh"`, `"number": 470`} {
		if !strings.Contains(out, want) {
			t.Errorf("board --json does not contain %q:\n%s", want, out)
		}
	}
}

// TestBoardRefusesAStrayArgument pins the invocation rule: these verbs take --json and
// nothing else, so a stray word is a usage error rather than something quietly ignored.
func TestBoardRefusesAStrayArgument(t *testing.T) {
	f := newFakeCoord()
	_, _, status := runCoordinatorVerb(t, boardCommand, f.deps(), "440")
	if status == 0 {
		t.Fatal("board with a stray argument = 0, want a usage failure")
	}
	if len(f.calls) != 0 {
		t.Errorf("the board read %v before refusing the invocation", f.calls)
	}
}

// TestCollisionsFindsTheWave pins the collisions verb: the serialization points are flagged
// apart from ordinary overlap, and two lanes in one point is the wave's binding constraint.
func TestCollisionsFindsTheWave(t *testing.T) {
	f := newFakeCoord()
	f.mergeBase = "734d7170aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f.worktrees = `worktree D:/src/wt-438
HEAD aaaa
branch refs/heads/chore/438-gate-board

worktree D:/src/wt-490
HEAD bbbb
branch refs/heads/chore/490-collisions
`
	f.diffs["D:/src/wt-438"] = "--- a/CHANGELOG.md\n+++ b/CHANGELOG.md\n@@ -30,0 +31,2 @@\n+x\n+y\n" +
		"--- a/internal/build/board/board.go\n+++ b/internal/build/board/board.go\n@@ -1,0 +2,3 @@\n+a\n"
	f.diffs["D:/src/wt-490"] = "--- a/CHANGELOG.md\n+++ b/CHANGELOG.md\n@@ -31,0 +32,1 @@\n+z\n" +
		"--- a/internal/build/board/parse.go\n+++ b/internal/build/board/parse.go\n@@ -1,0 +2,3 @@\n+b\n"

	out, errOut, status := runCoordinatorVerb(t, collisionsCommand, f.deps())
	if status != 0 {
		t.Fatalf("collisions = %d, want 0\n%s%s", status, out, errOut)
	}
	for _, want := range []string{
		"serialization points",
		"!! CHANGELOG.md",
		"chore/438-gate-board chore/490-collisions",
		"internal/build/policy/policy.go", // listed even untouched
		"file overlaps (0)",               // the only shared file is a serialization point
	} {
		if !strings.Contains(out, want) {
			t.Errorf("collisions output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "internal/build/board/board.go chore") {
		t.Errorf("a non-shared file appeared as an overlap:\n%s", out)
	}
}

// TestCollisionsReportsAWorktreeWithNothingToLand pins the honest note for a worktree whose
// diff against the base ref is empty: it is not a lane in the wave, and the report says so
// rather than printing an overlap with nothing in it.
func TestCollisionsReportsAWorktreeWithNothingToLand(t *testing.T) {
	f := newFakeCoord()
	f.mergeBase = "734d7170aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f.worktrees = "worktree D:/src/wt-438\nHEAD aaaa\nbranch refs/heads/chore/438\n"

	out, _, status := runCoordinatorVerb(t, collisionsCommand, f.deps())
	if status != 0 {
		t.Fatalf("collisions = %d, want 0", status)
	}
	if !strings.Contains(out, "D:/src/wt-438: no changes against e25e7ef4e33a") {
		t.Errorf("collisions did not report the empty worktree:\n%s", out)
	}
}

// TestCollisionsReportsNoMergeBase pins the failure a lane whose history has no shared
// ancestor produces: the report names it rather than dropping the lane silently.
func TestCollisionsReportsNoMergeBase(t *testing.T) {
	f := newFakeCoord()
	f.worktrees = "worktree D:/src/wt-438\nHEAD aaaa\nbranch refs/heads/chore/438\n"

	out, _, status := runCoordinatorVerb(t, collisionsCommand, f.deps())
	if status != 0 {
		t.Fatalf("collisions = %d, want 0", status)
	}
	if !strings.Contains(out, "no merge base") {
		t.Errorf("collisions did not report the missing merge base:\n%s", out)
	}
}

// seedLanding gives the stub a merged landing that holds every line, which each
// verify-landing case departs from by changing one reading.
func seedLanding(f *fakeCoord) {
	const head = "aaaa1111bbbb2222cccc3333dddd4444eeee5555"
	f.tags["tag0001"] = "branch=chore/438-gate-board worktree=wt-438"
	f.refs = "refs/lane/438 tag0001\n"
	f.pullsByNumber["511"] = fmt.Sprintf(`{
	  "number": 511, "state": "MERGED", "headRefName": "chore/438-gate-board",
	  "headRefOid": %q, "body": "Closes #438\n", "mergeStateStatus": "CLEAN",
	  "mergeCommit": {"oid": "e25e7ef4e33a247bec43bcee844fda5ec8c2bdc6"}
	}`, head)
	f.issueViews["438"] = `{"state": "CLOSED", "body": "## Home\n\n` + "`internal/build/board`" + ` and ` + "`CHANGELOG.md`" + `\n"}`
	f.searched = `[{"number": 511, "state": "MERGED"}]`
	// One parent: a squash.
	f.parents = "e25e7ef4e33a247bec43bcee844fda5ec8c2bdc6 734d7170aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f.showFiles = "internal/build/board/board.go\nCHANGELOG.md\n"
	// A frontmatter bump in one of the changed versioned files.
	f.diffsByPath["CHANGELOG.md"] = "@@ -1,0 +2,1 @@\n+version: v2\n"
	f.signed[head] = true
	f.remotes["refs/gate/"+head] = "1111111111111111111111111111111111111111"
	// No lane ref on origin: the lane is released. The worktree list names no lane either.
	f.remotes["refs/lane/438"] = ""
	f.worktrees = "worktree D:/src/go-cask\nHEAD e25e7ef4\nbranch refs/heads/main\n"
}

// TestVerifyLandingHoldsEveryLine pins the six checks against a landing that satisfies all of
// them, read from the authorities the verb asks.
func TestVerifyLandingHoldsEveryLine(t *testing.T) {
	f := newFakeCoord()
	seedLanding(f)

	out, errOut, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), "511")
	if status != 0 {
		t.Fatalf("verify-landing = %d, want 0\n%s%s", status, out, errOut)
	}
	if !strings.Contains(out, "6 of 6 line(s) hold") {
		t.Errorf("verify-landing = \n%s\nwant every line to hold", out)
	}
	for _, want := range []string{
		"merged squash, body cites Closes #438",
		"issue #438 is CLOSED",
		"every one of 2 changed file(s) is under internal/build/board",
		"with a CHANGELOG bullet and a frontmatter bump",
		"verifies as a signed commit and refs/gate/aaaa1111bbbb exists on origin",
		"the claim ref is gone and no worktree holds the lane",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verify-landing does not say %q:\n%s", want, out)
		}
	}
}

// TestVerifyLandingFailsLineByLine pins each line failing from its own authority, and that a
// failure is reported rather than passed.
func TestVerifyLandingFailsLineByLine(t *testing.T) {
	cases := []struct {
		name     string
		change   func(*fakeCoord)
		wantLine string
		evidence string
	}{
		{
			name: "the merge is not a squash",
			change: func(f *fakeCoord) {
				f.parents = "e25e7ef4 734d7170 1111111111111111111111111111111111111111"
			},
			wantLine: "the pull request merged as a squash",
			evidence: "not a squash",
		},
		{
			name: "the issue is still open",
			change: func(f *fakeCoord) {
				f.issueViews["438"] = `{"state": "OPEN", "body": "## Home\n\n` + "`internal/build/board`" + ` and ` + "`CHANGELOG.md`" + `\n"}`
			},
			wantLine: "the issue is CLOSED",
			evidence: "issue #438 is OPEN",
		},
		{
			name: "a drive-by file",
			change: func(f *fakeCoord) {
				f.showFiles = "internal/build/board/board.go\ncas/backend/fs/fs.go\nCHANGELOG.md\n"
			},
			wantLine: "the merged file list stays within the issue's scope",
			evidence: "cas/backend/fs/fs.go",
		},
		{
			name:     "no changelog bullet",
			change:   func(f *fakeCoord) { f.showFiles = "internal/build/board/board.go\n" },
			wantLine: "the change carried the docs it needed",
			evidence: "no CHANGELOG bullet",
		},
		{
			name:     "an unsigned head",
			change:   func(f *fakeCoord) { f.signed = map[string]bool{} },
			wantLine: "the head was signed and its gate receipt was published",
			evidence: "is not a verified signature",
		},
		{
			name:     "no published receipt",
			change:   func(f *fakeCoord) { f.remotes = map[string]string{} },
			wantLine: "the head was signed and its gate receipt was published",
			evidence: "is not on origin",
		},
		{
			name: "the lane is still held",
			change: func(f *fakeCoord) {
				f.refs = "refs/lane/438 tag0001\n"
				f.remotes["refs/lane/438"] = "9999999999999999999999999999999999999999"
			},
			wantLine: "the lane was released and its worktree is gone",
			evidence: "the claim ref still exists",
		},
		{
			name: "the worktree survives",
			change: func(f *fakeCoord) {
				f.worktrees = "worktree D:/src/go-cask/.worktrees/wt-438\nHEAD aaaa\nbranch refs/heads/chore/438-gate-board\n"
			},
			wantLine: "the lane was released and its worktree is gone",
			evidence: "a worktree still holds the lane",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCoord()
			seedLanding(f)
			tc.change(f)

			out, _, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), "511")
			if status != 1 {
				t.Fatalf("verify-landing = %d, want 1 (a reported failure)\n%s", status, out)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("verify-landing does not name the failing line %q:\n%s", tc.wantLine, out)
			}
			if !strings.Contains(out, tc.evidence) {
				t.Errorf("verify-landing does not carry the evidence %q:\n%s", tc.evidence, out)
			}
			if !strings.Contains(out, "not verified:") {
				t.Errorf("verify-landing printed no closing verdict:\n%s", out)
			}
		})
	}
}

// TestVerifyLandingResolvesAnIssue pins the argument's other form: a coordinator holding the
// issue number reaches the same landing its pull request number would.
func TestVerifyLandingResolvesAnIssue(t *testing.T) {
	f := newFakeCoord()
	seedLanding(f)

	out, errOut, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), "438")
	if status != 0 {
		t.Fatalf("verify-landing 438 = %d, want 0\n%s%s", status, out, errOut)
	}
	if !strings.Contains(out, "verify-landing PR #511") {
		t.Errorf("verify-landing 438 did not resolve to PR #511:\n%s", out)
	}
}

// TestVerifyLandingRefusesAnUnknownSubject pins the failure a coordinator reads when neither
// reading resolves: the command says which two things it looked for.
func TestVerifyLandingRefusesAnUnknownSubject(t *testing.T) {
	f := newFakeCoord()
	_, errOut, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), "9999")
	if status == 0 {
		t.Fatal("verify-landing of an unknown subject = 0, want a failure")
	}
	if !strings.Contains(errOut, "no pull request #9999") {
		t.Errorf("the failure does not say what was looked for: %s", errOut)
	}
}

// TestVerifyLandingRejectsBadInvocations pins the usage rules: a missing subject, a stray
// argument, and a non-number are all the caller's mistake, not a rule failure.
func TestVerifyLandingRejectsBadInvocations(t *testing.T) {
	f := newFakeCoord()
	for _, args := range [][]string{
		{},
		{"511", "512"},
		{"--nope"},
		{"abc"},
		{"0"},
	} {
		_, _, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), args...)
		if status == 0 {
			t.Errorf("verify-landing %v = 0, want a usage failure", args)
		}
	}
}

// TestReadJSONFlag pins the flag reader the board and collisions share.
func TestReadJSONFlag(t *testing.T) {
	asJSON, err := readJSONFlag(nil, "board")
	if err != nil || asJSON {
		t.Errorf("no flags = (%t, %v), want (false, nil)", asJSON, err)
	}
	asJSON, err = readJSONFlag([]string{"--json"}, "board")
	if err != nil || !asJSON {
		t.Errorf("--json = (%t, %v), want (true, nil)", asJSON, err)
	}
	if _, err := readJSONFlag([]string{"440"}, "board"); err == nil {
		t.Error("a positional argument = nil error, want a usage failure")
	}
	if !wantsHelp([]string{"--help"}) || !wantsHelp([]string{"help"}) {
		t.Error("a help flag was not recognised")
	}
	if wantsHelp([]string{"--json"}) {
		t.Error("--json was read as a help flag")
	}
}

// TestGHPullViewParsesTheForgeListing pins the reader against the CLI's own shapes: the
// merged state, the body's citation, the head commit and the merge commit, with the parents
// field present and absent — the CLI omits it unless the commit is expanded.
func TestGHPullViewParsesTheForgeListing(t *testing.T) {
	f := newFakeCoord()
	f.pullsByNumber["511"] = `{
	  "number": 511, "state": "MERGED", "headRefName": "chore/344-fresh-base",
	  "headRefOid": "aaaa1111bbbb2222cccc3333dddd4444eeee5555",
	  "body": "Fixes #344.\n", "mergeStateStatus": "CLEAN",
	  "mergeCommit": {"oid": "e25e7ef4e33a247bec43bcee844fda5ec8c2bdc6"}
	}`

	pull, err := ghPullView(f.deps(), "owner/name", 511)
	if err != nil {
		t.Fatalf("ghPullView: %v", err)
	}
	if pull.Number != 511 || pull.State != "MERGED" || pull.MergeOID != "e25e7ef4e33a247bec43bcee844fda5ec8c2bdc6" {
		t.Errorf("pull = %+v, want the number, state and merge commit", pull)
	}
	if pull.HeadOID != "aaaa1111bbbb2222cccc3333dddd4444eeee5555" {
		t.Errorf("head = %q, want the head commit", pull.HeadOID)
	}
	if pull.IssueNumber != 344 {
		t.Errorf("cited issue = %d, want #344 read out of the body", pull.IssueNumber)
	}
	if pull.MergeState != "CLEAN" {
		t.Errorf("merge state = %q, want the forge's", pull.MergeState)
	}

	// A number the forge does not know is a failure, not an empty pull request.
	if _, err := ghPullView(f.deps(), "owner/name", 9999); err == nil {
		t.Error("ghPullView of an unknown pull request = nil error, want a failure")
	}
	// A listing this build cannot read is a failure too.
	f.pullsByNumber["512"] = "not json"
	if _, err := ghPullView(f.deps(), "owner/name", 512); err == nil {
		t.Error("ghPullView of a non-listing = nil error, want a failure")
	}
}

// TestVersionBumpedAndContainsPath pins the two readings the docs check is built from: a
// frontmatter line in the landing's own diff, and a path in the changed-file list.
func TestVersionBumpedAndContainsPath(t *testing.T) {
	f := newFakeCoord()
	f.diffsByPath["docs/specs/landing.md"] = "@@ -4,0 +5,1 @@\n+version: v6\n"
	f.diffsByPath["internal/build/board/README.md"] = "@@ -1,0 +2,1 @@\n+prose, not a version\n"

	files := []string{"internal/build/board/README.md", "docs/specs/landing.md"}
	if !versionBumped(f.deps(), files, "aaaa") {
		t.Error("versionBumped = false, want the changed frontmatter line found")
	}
	if versionBumped(f.deps(), files, "") {
		t.Error("versionBumped with no commit = true, want false: nothing was read")
	}
	if versionBumped(f.deps(), []string{"internal/build/board/README.md"}, "aaaa") {
		t.Error("versionBumped = true for a file that moved no version line")
	}
	if !containsPath(files, "docs/specs/landing.md") || containsPath(files, "CHANGELOG.md") {
		t.Error("containsPath did not read the changed-file list")
	}
}

// TestVerifyLandingJSON pins the machine-readable form of the six lines.
func TestVerifyLandingJSON(t *testing.T) {
	f := newFakeCoord()
	seedLanding(f)

	out, _, status := runCoordinatorVerb(t, verifyLandingCommand, f.deps(), "511", "--json")
	if status != 0 {
		t.Fatalf("verify-landing --json = %d, want 0\n%s", status, out)
	}
	for _, want := range []string{`"subject": "PR #511"`, `"verified": true`, `"name": "the issue is CLOSED"`} {
		if !strings.Contains(out, want) {
			t.Errorf("verify-landing --json does not contain %q:\n%s", want, out)
		}
	}
}

// TestCollisionsJSON pins the machine-readable form of the matrix.
func TestCollisionsJSON(t *testing.T) {
	f := newFakeCoord()
	f.worktrees = "worktree D:/src/wt-438\nHEAD aaaa\nbranch refs/heads/chore/438\n"

	out, _, status := runCoordinatorVerb(t, collisionsCommand, f.deps(), "--json")
	if status != 0 {
		t.Fatalf("collisions --json = %d, want 0", status)
	}
	for _, want := range []string{`"points"`, `"CHANGELOG.md"`, `"overlaps"`} {
		if !strings.Contains(out, want) {
			t.Errorf("collisions --json does not contain %q:\n%s", want, out)
		}
	}
}

// TestCoordinatorVerbHelp pins that each verb answers its own help without reading anything.
func TestCoordinatorVerbHelp(t *testing.T) {
	f := newFakeCoord()
	for _, tc := range []struct {
		verb func([]string, io.Writer, io.Writer, prLaneDeps) error
		want string
	}{
		{boardCommand, boardUsage},
		{collisionsCommand, collisionsUsage},
		{verifyLandingCommand, verifyLandingUsage},
	} {
		out, _, status := runCoordinatorVerb(t, tc.verb, f.deps(), "--help")
		if status != 0 || !strings.Contains(out, tc.want) {
			t.Errorf("help = %d, %q; want 0 with %q", status, out, tc.want)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a help flag read the world: %v", f.calls)
	}
}

// TestIssueOfBranchAndCandidates pins the branch-to-issue reading: go-cask's branch namespace
// carries the issue number, and `landing.BranchNamesIssue` is the rule that decides which digit
// run in a branch name it is.
func TestIssueOfBranchAndCandidates(t *testing.T) {
	cases := []struct {
		branch string
		want   int
	}{
		{branch: "chore/438-gate-board", want: 438},
		{branch: "fix/348-viewer-security-session", want: 348},
		{branch: "perf/372-cas-core-pipeline", want: 372},
		{branch: "main", want: 0},
		{branch: "", want: 0},
	}
	for _, tc := range cases {
		if got := issueOfBranch(tc.branch); got != tc.want {
			t.Errorf("issueOfBranch(%q) = %d, want %d", tc.branch, got, tc.want)
		}
	}
	if got := strings.Join(issueCandidates("fix/348-viewer-3d-x"), ","); got != "348,3" {
		t.Errorf("issueCandidates = %q, want both digit runs", got)
	}
	if got := issueCandidates("main"); len(got) != 0 {
		t.Errorf("issueCandidates(main) = %v, want none", got)
	}
}

// TestIssueCited pins the body citation reader: both landing words, and the first number that
// follows one.
func TestIssueCited(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{body: "Closes #438\n", want: 438},
		{body: "Fixes #438.", want: 438},
		{body: "Refs #438", want: 0},
		{body: "", want: 0},
		{body: "closes #", want: 0},
	}
	for _, tc := range cases {
		if got := issueCited(tc.body); got != tc.want {
			t.Errorf("issueCited(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}

// TestSquashMerged pins the merge-method reading: two fields on a `git rev-list --parents -1`
// line is a squash, three is a merge commit, and a commit git cannot resolve is unproven.
func TestSquashMerged(t *testing.T) {
	cases := []struct {
		name    string
		parents string
		merge   string
		want    bool
	}{
		{name: "a squash", parents: "aaa bbb", merge: "aaa", want: true},
		{name: "a merge commit", parents: "aaa bbb ccc", merge: "aaa", want: false},
		{name: "no merge commit at all", parents: "aaa bbb", merge: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCoord()
			f.parents = tc.parents
			pull := pullView{MergeOID: tc.merge}
			if got := pull.squashMerged(f.deps()); got != tc.want {
				t.Errorf("squashMerged = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestGatedCommits pins the evidence rule against a clone-shaped fixture: a commit is gated
// only when the ledger names it AND a receipt for it exists — one without the other is not the
// evidence a green gate leaves (docs/specs/coordination.md §3).
func TestGatedCommits(t *testing.T) {
	common := t.TempDir()
	if err := os.MkdirAll(filepath.Join(common, "gate-receipts"), 0o755); err != nil {
		t.Fatalf("creating the receipt directory: %v", err)
	}
	verified := "aaaa1111bbbb2222cccc3333dddd4444eeee5555"
	claimed := "bbbb2222cccc3333dddd4444eeee5555ffff6666"
	ledger := verified + "\n" + claimed + "\nnot-a-commit\n"
	if err := os.WriteFile(filepath.Join(common, "verify.ok"), []byte(ledger), 0o644); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	if err := os.WriteFile(filepath.Join(common, "gate-receipts", verified+".receipt"), []byte("receipt"), 0o644); err != nil {
		t.Fatalf("writing the receipt: %v", err)
	}

	commits := gatedCommits(common, []board.PullRequest{
		{Number: 1, Branch: "b", HeadOID: verified},
		{Number: 2, Branch: "c", HeadOID: claimed},
		{Number: 3, Branch: "d", HeadOID: "cccc3333dddd4444eeee5555ffff6666aaaa7777"},
		{Number: 4, Branch: "e"},
	})
	if !commits[verified] {
		t.Error("a commit the ledger names and a receipt names was not reported as gated")
	}
	if commits[claimed] {
		t.Error("a commit the ledger names with no receipt was reported as gated")
	}
	if len(commits) != 3 {
		t.Errorf("gatedCommits read %d commits, want one per head commit", len(commits))
	}
	if got := gatedCommits(common, nil); len(got) != 0 {
		t.Errorf("gatedCommits with no pull requests = %v, want nothing", got)
	}
}
