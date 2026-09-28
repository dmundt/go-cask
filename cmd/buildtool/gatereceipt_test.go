package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/receipt"
)

// gateReceiptShared is one throwaway signing identity for this whole test binary.
//
// The command signs by shelling out to `git commit-tree -S`, and that child inherits THIS
// process's environment — which is exactly how the shell behaviour test drove it, exporting
// GIT_CONFIG_* before it ran the helper. A per-fixture environment cannot stand in for that:
// the command reads no environment of a test's choosing, so the developer's own
// `user.signingkey` would sign every receipt instead.
//
// The environment is therefore set once, at first use, and left in place. `t.Setenv` says the
// same thing but cannot be combined with t.Parallel, and these fixtures are independent
// enough to run in parallel. GIT_CONFIG_COUNT=2 makes git ignore any KEY_n it inherited from
// a caller, so a harness that configures git through the environment cannot reach in either.
type gateReceiptShared struct {
	once sync.Once
	key  string
	env  []string
	err  error
}

// gateReceiptSigner is the shared identity, created the first time a fixture is built.
var gateReceiptSigner = &gateReceiptShared{}

// setup creates the shared key and installs its signing configuration in the process
// environment. It reports a failure rather than skipping, because every caller has already
// checked that ssh-keygen exists.
func (s *gateReceiptShared) setup(t *testing.T) {
	t.Helper()
	s.once.Do(func() {
		dir, err := os.MkdirTemp("", "gate-receipt-signer")
		if err != nil {
			s.err = fmt.Errorf("creating the signing key's directory: %w", err)
			return
		}
		key := filepath.Join(dir, "shared")
		cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "gate@test", "-f", key)
		if output, err := cmd.CombinedOutput(); err != nil {
			s.err = fmt.Errorf("ssh-keygen: %w\n%s", err, output)
			return
		}
		s.key = key
		s.env = []string{
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=gpg.format", "GIT_CONFIG_VALUE_0=ssh",
			"GIT_CONFIG_KEY_1=user.signingkey", "GIT_CONFIG_VALUE_1=" + key,
		}
		for _, entry := range s.env {
			name, value, _ := strings.Cut(entry, "=")
			if err := os.Setenv(name, value); err != nil {
				s.err = fmt.Errorf("setting %s: %w", name, err)
				return
			}
		}
	})
	if s.err != nil {
		t.Fatalf("%v", s.err)
	}
}

// gateReceiptFixture is a throwaway repository with a bare `origin` beside it and two
// throwaway SSH identities.
//
// Everything it signs with is a throwaway key: the process-wide one above for whatever the
// command itself signs, and its own pair for the objects a test builds by hand. Nothing here
// reads — or changes — the developer's git configuration, and nothing needs a network:
// `origin` is a directory. What this command decides is which receipts CI may trust, and that
// decision has to be exercised against real git and real signatures rather than against a
// stub of them.
type gateReceiptFixture struct {
	t        *testing.T
	work     string
	repo     string
	origin   string
	trusted  string
	other    string
	keyEnv   []string
	base     string
	head     string
	headTree string
	docs     string
	docsTree string
}

// gateReceiptExitCode turns the error a verb returned into the exit status a process would
// have exited with, and writes the message the way main does. It is the one place a test
// needs main's dispatch: a refusal that landed on the wrong stream, or with the wrong
// status, is as wrong as the wrong verdict.
func gateReceiptExitCode(err error, errOut *bytes.Buffer) int {
	switch {
	case err == nil:
		return 0
	default:
		var status statusError
		var usage usageError
		switch {
		case errors.As(err, &status):
			if status.message != "" {
				errOut.WriteString(status.message + "\n")
			}
			return status.code
		case errors.As(err, &usage):
			// The command prints its own report for the mistakes it catches itself, and
			// main prints the message it returns; a mistake the flag set caught has only
			// this one, so the message goes out here in every case.
			errOut.WriteString(usage.message + "\n")
			return 2
		default:
			errOut.WriteString(err.Error() + "\n")
			return 1
		}
	}
}

// gateReceiptAllowList renders an allowed_signers line for a public key:
// `<principal> namespaces="git" <keytype> <key>`, which is the format git checks an SSH
// signature against.
func gateReceiptAllowList(t *testing.T, publicKey string) string {
	t.Helper()
	content, err := os.ReadFile(publicKey)
	if err != nil {
		t.Fatalf("reading %s: %v", publicKey, err)
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		t.Fatalf("%s is not a public key: %q", publicKey, content)
	}
	return fields[2] + " namespaces=\"git\" " + fields[0] + " " + fields[1] + "\n"
}

// gateReceiptEnv is the environment a fixture's git runs with: this process's environment
// with every inherited GIT_CONFIG_* entry removed, and the fixture's own signing
// configuration appended.
//
// Removing them is not tidiness. GIT_CONFIG_COUNT and its numbered companions let a caller —
// a harness, a wrapper script, another tool — configure git through the environment, and a
// duplicate KEY_0/VALUE_0 pair leaves which one wins up to the operating system. This
// fixture's git has its own key to sign with, and it must be the one in effect.
func gateReceiptEnv(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_CONFIG_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}

// gateReceiptGit runs one git command in a directory and fails the test when it does not
// succeed. The environment is the difference the fixture cares about: `git commit -S` and
// `git commit-tree -S` read the signing configuration from it, and a command that lost it
// would silently sign with whatever key the machine has.
func gateReceiptGit(t *testing.T, dir string, extra []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gateReceiptEnv(extra)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// git runs one git command in the fixture repository with the fixture's signing environment.
func (f *gateReceiptFixture) git(args ...string) string {
	f.t.Helper()
	return gateReceiptGit(f.t, f.repo, f.keyEnv, args...)
}

// commit records one file as one signed commit and returns the commit's full object name.
func (f *gateReceiptFixture) commit(path, content string) string {
	f.t.Helper()
	full := filepath.Join(f.repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatalf("creating %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content+"\n"), 0o644); err != nil {
		f.t.Fatalf("writing %s: %v", path, err)
	}
	f.git("add", "--", path)
	f.git("commit", "-S", "-q", "-m", "add "+path)
	return f.git("rev-parse", "HEAD")
}

// run drives one invocation and reports its streams and exit status.
//
// The fixture's repository is passed in rather than resolved from the working directory,
// because that is the difference the production caller makes: the gate's step starts the
// command in the worktree being verified, so the repository the command speaks about is its
// working directory. A test cannot say the same without moving the process, which parallel
// tests would fight over.
func (f *gateReceiptFixture) run(args ...string) (stdout, stderr string, status int) {
	f.t.Helper()
	var out, errOut bytes.Buffer
	err := gateReceiptCommand(f.repo, args, &out, &errOut, gateReceiptDeps{scopeDocs: f.scopeDocs})
	// The status is resolved before the streams are read: main reports a usage error's
	// message itself, so the mapping writes to the same buffer this returns. Reading the
	// buffer first would report every usage mistake as an empty complaint.
	status = gateReceiptExitCode(err, &errOut)
	return out.String(), errOut.String(), status
}

// scopeDocs answers the scope question the way the engine's `docs_only` rule does for the
// one arm a receipt's scope depends on: a change that touches a Go file or a module file is
// a code change, and anything else is a documentation change.
//
// The rule itself is not under test here — the receipt machinery is — but the classification
// still runs against the fixture's real change rather than a canned answer, so a receipt for
// a Go change cannot be accepted because a test told the command to think otherwise.
func (f *gateReceiptFixture) scopeDocs(root, base, sha string) (bool, error) {
	f.t.Helper()
	listed := strings.TrimSpace(f.git("diff", "--name-only", base, sha))
	if listed == "" {
		// Nothing to classify is not a documentation change: a receipt records the scope of
		// a change it measured, and the engine's own rule answers false here too.
		return false, nil
	}
	for _, path := range splitLines(listed) {
		if strings.HasSuffix(path, ".go") || path == "go.mod" || path == "go.sum" {
			return false, nil
		}
	}
	return true, nil
}

// create writes a receipt for a commit, as the gate's own step does.
func (f *gateReceiptFixture) create(sha, scope, checks string, extra ...string) (string, string, int) {
	f.t.Helper()
	args := append([]string{"create", "--sha", sha, "--base", f.base, "--scope", scope, "--checks", checks}, extra...)
	return f.run(args...)
}

// publish signs the local receipt for a commit and pushes it to the fixture's origin.
func (f *gateReceiptFixture) publish(sha string) (string, string, int) {
	f.t.Helper()
	return f.run("publish", "--sha", sha, "--remote", "origin")
}

// verify checks a commit's receipt against the tree a checkout would have, as CI does.
func (f *gateReceiptFixture) verify(sha, tree string, extra ...string) (string, string, int) {
	f.t.Helper()
	args := append([]string{"verify", "--sha", sha, "--tree", tree, "--base", f.base,
		"--signers", filepath.Join(f.work, "allow-trusted")}, extra...)
	return f.run(args...)
}

// receipt is the local receipt file for a commit, as this clone wrote it.
func (f *gateReceiptFixture) receipt(sha string) string {
	f.t.Helper()
	common := f.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	return readFileOrEmpty(filepath.Join(common, "gate-receipts", sha+".receipt"))
}

// receiptBody is the receipt's text without the trailing newline the file carries, which is
// the form a commit message has and therefore the form show prints.
func (f *gateReceiptFixture) receiptBody(sha string) string {
	f.t.Helper()
	return strings.TrimSuffix(f.receipt(sha), "\n")
}

// publishedRef is the object the fixture's origin holds for a commit's receipt ref.
func (f *gateReceiptFixture) publishedRef(sha string) string {
	f.t.Helper()
	fields := strings.Fields(f.git("ls-remote", "origin", "refs/gate/"+sha))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// setPublishedRef points the local receipt ref at an object a test built by hand, which is
// how the refusal cases produce a receipt nobody could have written through the command.
func (f *gateReceiptFixture) setPublishedRef(sha, object string) {
	f.t.Helper()
	f.git("update-ref", "refs/gate/"+sha, object)
}

// commitTree builds a commit object directly — optionally signed — on a chosen parent and
// tree, so a test can produce the shapes a verification must refuse: a receipt on the wrong
// parent, on the wrong tree, or signed by the wrong key. An empty parent builds a root
// commit, which is the only honest way to make a commit that is not an ancestor of another.
func (f *gateReceiptFixture) commitTree(message, tree, parent string, sign bool) string {
	f.t.Helper()
	args := []string{"commit-tree"}
	if sign {
		args = append(args, "-S")
	}
	args = append(args, "-m", message, tree)
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return f.git(args...)
}

// otherKeyEnv is the fixture's signing environment with the other identity's key, which is
// how a test signs as somebody the allow-list does not trust.
func (f *gateReceiptFixture) otherKeyEnv() []string {
	f.t.Helper()
	env := make([]string, 0, len(f.keyEnv))
	for _, entry := range f.keyEnv {
		if strings.HasPrefix(entry, "GIT_CONFIG_VALUE_1=") {
			entry = "GIT_CONFIG_VALUE_1=" + f.other
		}
		env = append(env, entry)
	}
	return env
}

// newGateReceiptFixture builds the repository, the keys and the remote.
func newGateReceiptFixture(t *testing.T) *gateReceiptFixture {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed; a receipt is signed with throwaway SSH keys")
	}

	work := t.TempDir()
	keys := filepath.Join(work, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatalf("creating %s: %v", keys, err)
	}
	f := &gateReceiptFixture{
		t:       t,
		work:    work,
		repo:    filepath.Join(work, "repo"),
		origin:  filepath.Join(work, "origin.git"),
		trusted: filepath.Join(keys, "trusted"),
		other:   filepath.Join(keys, "other"),
	}
	// The identity the command signs with is the process-wide one; the fixture's own pair is
	// for the objects a test builds by hand, and for the untrusted-signer case it is the key
	// the allow-list must NOT name.
	gateReceiptSigner.setup(t)
	for _, identity := range []struct{ name, comment string }{
		{"trusted", "gate@test"},
		{"other", "other@test"},
	} {
		key := filepath.Join(keys, identity.name)
		cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", identity.comment, "-f", key)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %s: %v\n%s", identity.name, err, output)
		}
		allow := filepath.Join(work, "allow-"+identity.name)
		if err := os.WriteFile(allow, []byte(gateReceiptAllowList(t, key+".pub")), 0o644); err != nil {
			t.Fatalf("writing %s: %v", allow, err)
		}
	}
	// A published receipt is signed by the shared identity, so the allow-list a verification
	// is pointed at has to name it. Both keys in one list is also what a repository with more
	// than one trusted signer looks like.
	trustedList := gateReceiptAllowList(t, f.trusted+".pub") +
		gateReceiptAllowList(t, gateReceiptSigner.key+".pub")
	if err := os.WriteFile(filepath.Join(work, "allow-trusted"), []byte(trustedList), 0o644); err != nil {
		t.Fatalf("writing the trusted allow-list: %v", err)
	}

	// The fixture's own git signs with the fixture's own key, so the objects a test builds by
	// hand carry the identity that test intends. Author and committer ride the environment
	// with it, for the same reason: no config file is touched.
	f.keyEnv = append(append([]string{}, f.keyEnv...),
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=gpg.format", "GIT_CONFIG_VALUE_0=ssh",
		"GIT_CONFIG_KEY_1=user.signingkey", "GIT_CONFIG_VALUE_1="+f.trusted,
		"GIT_AUTHOR_NAME=Gate Test", "GIT_AUTHOR_EMAIL=gate@test",
		"GIT_COMMITTER_NAME=Gate Test", "GIT_COMMITTER_EMAIL=gate@test",
	)
	if err := os.MkdirAll(f.repo, 0o755); err != nil {
		t.Fatalf("creating %s: %v", f.repo, err)
	}
	gateReceiptGit(t, f.repo, nil, "init", "-q", "-b", "main", ".")
	gateReceiptGit(t, f.repo, nil, "config", "user.email", "gate@test")
	gateReceiptGit(t, f.repo, nil, "config", "user.name", "Gate Test")
	gateReceiptGit(t, f.repo, nil, "config", "commit.gpgsign", "false")
	gateReceiptGit(t, f.repo, nil, "init", "-q", "--bare", f.origin)
	gateReceiptGit(t, f.repo, nil, "remote", "add", "origin", f.origin)

	// The repository's own allow-list is out of reach for a fixture, and a test must not
	// write into it, so every verification here passes `--signers` — the same override a
	// runner uses — rather than an environment variable a parallel test would share.

	// origin/main is what a create defaults its base to and where publish pushes the
	// receipt, so the first commit is pushed and fetched before anything else happens.
	f.base = f.commit("README.md", "# probe")
	f.git("push", "-q", "origin", "main")
	f.git("fetch", "-q", "origin")
	f.head = f.commit("main.go", "package main")
	f.headTree = f.git("rev-parse", f.head+"^{tree}")
	f.docs = f.commit("docs/note.md", "# note")
	f.docsTree = f.git("rev-parse", f.docs+"^{tree}")
	return f
}

// writeReceipt writes the fixture's standard full-scope receipt for its head commit and
// publishes it, which is the state most of the verification cases start from.
func (f *gateReceiptFixture) writeReceipt() {
	f.t.Helper()
	if _, stderr, status := f.create(f.head, "full", "gofmt go-test-race govulncheck coverage-tiers", "--coverage-tiers", "37"); status != 0 {
		f.t.Fatalf("create = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.publish(f.head); status != 0 {
		f.t.Fatalf("publish = %d, want 0: %s", status, stderr)
	}
}

// ---------------------------------------------------------------------------
// create
// ---------------------------------------------------------------------------

// TestGateReceiptCreateWritesTheReceipt pins what a receipt records, field by field, against
// the record the engine renders for the same facts. It also pins that create says nothing on
// standard error, because the gate's own step reads its standard output as the path.
func TestGateReceiptCreateWritesTheReceipt(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	stdout, stderr, status := f.create(f.head, "full", "gofmt go-test-race govulncheck coverage-tiers", "--coverage-tiers", "37")
	if status != 0 {
		t.Fatalf("create = %d, want 0\n%s", status, stderr)
	}

	common := f.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	wantPath := filepath.Join(common, "gate-receipts", f.head+".receipt")
	if got := strings.TrimSpace(stdout); got != wantPath {
		t.Errorf("create printed %q, want the receipt's path %q", got, wantPath)
	}
	payload := f.receipt(f.head)
	if payload == "" {
		t.Fatal("create wrote no receipt file")
	}
	record, err := receipt.Parse(payload)
	if err != nil {
		t.Fatalf("the receipt create wrote does not parse: %v\n%s", err, payload)
	}

	wantDiff, err := gateReceiptPathHash(f.repo, f.base, f.head)
	if err != nil {
		t.Fatalf("hashing the change: %v", err)
	}
	want := receipt.Record{
		Commit:        f.head,
		Tree:          f.headTree,
		Base:          f.base,
		Diff:          wantDiff,
		Scope:         receipt.Full,
		Checks:        []string{"gofmt", "go-test-race", "govulncheck", "coverage-tiers"},
		CoverageTiers: "37",
		Go:            runtime.Version(),
		Runner:        gateReceiptRunner(),
		Run:           record.Run,
	}
	if got := receipt.Render(record); got != receipt.Render(want) {
		t.Errorf("create wrote\n%s\nwant\n%s", got, receipt.Render(want))
	}
	if !strings.HasPrefix(payload, receipt.Version+"\n") {
		t.Errorf("the receipt is not marked as %s:\n%s", receipt.Version, payload)
	}

	// Nothing may reach standard error on a success: the gate's step streams both, and a
	// note there reads as a complaint about a green run.
	if stderr != "" {
		t.Errorf("create wrote %q to standard error", stderr)
	}
}

// TestGateReceiptCreateDefaultsItsBaseToOriginMain pins the base a caller without one gets,
// which is the merge base the gate's own scope decision used.
func TestGateReceiptCreateDefaultsItsBaseToOriginMain(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	_, stderr, status := f.run("create", "--sha", f.head, "--scope", "full", "--checks", "gofmt")
	if status != 0 {
		t.Fatalf("create with no --base = %d, want 0: %s", status, stderr)
	}
	record, err := receipt.Parse(f.receipt(f.head))
	if err != nil {
		t.Fatalf("the receipt does not parse: %v", err)
	}
	if record.Base != f.base {
		t.Errorf("the receipt records base %s, want the merge base with origin/main, %s", record.Base, f.base)
	}
}

// TestGateReceiptCreateRefusals pins the mistakes create reports as usage errors. Each is a
// value a caller passes, so the case is a table.
func TestGateReceiptCreateRefusals(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	cases := []struct {
		name   string
		scope  string
		checks string
		want   string
	}{
		{
			name:   "a scope that is neither docs nor full",
			scope:  "sideways",
			checks: "gofmt",
			want:   "--scope must be docs or full",
		},
		{
			name:   "a check name the format cannot carry",
			scope:  "full",
			checks: "bad:name",
			want:   "invalid characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, stderr, status := f.create(f.head, tc.scope, tc.checks)
			if status != 2 {
				t.Fatalf("create with %s = %d, want the usage status 2\n%s", tc.name, status, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("the refusal %q does not say %q", stderr, tc.want)
			}
			if out != "" {
				t.Errorf("create wrote %q to standard output on a refusal", out)
			}
		})
	}
}

// TestGateReceiptCreateRefusesABaseThatIsNotACommit pins that a revision which resolves to
// nothing is the caller's mistake: a receipt built on a base that does not exist would be
// refused by every verification anyway.
func TestGateReceiptCreateRefusesABaseThatIsNotACommit(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	_, stderr, status := f.run("create", "--sha", f.head, "--base", "no-such-revision",
		"--scope", "full", "--checks", "gofmt")
	if status != 2 {
		t.Fatalf("create with an unresolvable base = %d, want 2\n%s", status, stderr)
	}
	if !strings.Contains(stderr, "base is not a commit") {
		t.Errorf("the refusal %q does not name the base", stderr)
	}
}

// ---------------------------------------------------------------------------
// publish
// ---------------------------------------------------------------------------

// TestGateReceiptPublishPushesASignedRefAndIsIdempotent pins the two properties the shell
// test pins: the ref arrives at the remote, and a second publish with the same evidence
// reports it already published without rewriting the ref.
func TestGateReceiptPublishPushesASignedRefAndIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	if _, stderr, status := f.create(f.head, "full", "gofmt go-test-race coverage-tiers"); status != 0 {
		t.Fatalf("create = %d, want 0: %s", status, stderr)
	}

	stdout, stderr, status := f.publish(f.head)
	if status != 0 {
		t.Fatalf("publish = %d, want 0\n%s", status, stderr)
	}
	published := f.publishedRef(f.head)
	if published == "" {
		t.Fatal("publish did not push refs/gate/<sha>")
	}
	if !strings.Contains(stdout, "refs/gate/"+f.head) {
		t.Errorf("publish reported %q, want the ref it pushed", stdout)
	}

	// The ref must be a commit built on the gated commit and carrying its tree, because
	// those are the two claims CI checks without reading the message.
	if parent := f.git("rev-parse", published+"^"); parent != f.head {
		t.Errorf("the published receipt is built on %s, want %s", parent, f.head)
	}
	if tree := f.git("rev-parse", published+"^{tree}"); tree != f.headTree {
		t.Errorf("the published receipt carries tree %s, want %s", tree, f.headTree)
	}
	// And it is signed by the fixture's own key: a receipt CI cannot verify is no evidence
	// at all.
	f.git("-c", "gpg.format=ssh",
		"-c", "gpg.ssh.allowedSignersFile="+filepath.Join(f.work, "allow-trusted"),
		"verify-commit", published)

	stdout, stderr, status = f.publish(f.head)
	if status != 0 {
		t.Fatalf("publish again = %d, want 0\n%s", status, stderr)
	}
	if !strings.Contains(stdout, "already published") {
		t.Errorf("publish again reported %q, want it to say the receipt is already published", stdout)
	}
	if got := f.publishedRef(f.head); got != published {
		t.Errorf("publish again moved the ref from %s to %s", published, got)
	}
}

// TestGateReceiptPublishReplacesARefWhoseEvidenceMoved pins the one rewrite this ref is
// allowed: a new gate run whose evidence differs replaces what CI reads, and says so.
func TestGateReceiptPublishReplacesARefWhoseEvidenceMoved(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()
	first := f.publishedRef(f.head)

	// The same commit, gated again with different evidence: one check fewer completed.
	if _, stderr, status := f.create(f.head, "full", "gofmt go-test-race coverage-tiers"); status != 0 {
		t.Fatalf("create = %d, want 0: %s", status, stderr)
	}
	stdout, stderr, status := f.publish(f.head)
	if status != 0 {
		t.Fatalf("publish = %d, want 0\n%s", status, stderr)
	}
	if !strings.Contains(stdout, "replaced the receipt") {
		t.Errorf("publish reported %q, want it to say the receipt was replaced", stdout)
	}
	if got := f.publishedRef(f.head); got == first {
		t.Error("publish did not replace a ref whose evidence moved")
	}
}

// TestGateReceiptPublishRefusesWithoutALocalReceipt pins the refusal a commit whose gate
// never ran gets: there is no evidence, which is exactly the state CI's fallback exists for.
func TestGateReceiptPublishRefusesWithoutALocalReceipt(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	_, stderr, status := f.publish(f.head)
	if status != 1 {
		t.Fatalf("publish with no local receipt = %d, want the refusal status 1\n%s", status, stderr)
	}
	if !strings.Contains(stderr, "no local gate receipt for "+f.head) {
		t.Errorf("the refusal %q does not name the commit and the remedy", stderr)
	}
	if !strings.HasPrefix(stderr, "gate-receipt: ") {
		t.Errorf("the refusal %q does not carry the command's name", stderr)
	}
}

// ---------------------------------------------------------------------------
// verify: the accept path
// ---------------------------------------------------------------------------

// TestGateReceiptVerifyAcceptsATrustedReceipt pins the fast path in full: a signed receipt
// whose parent is the commit, whose tree is the checked-out tree, whose base is an ancestor,
// whose diff hash still describes the change, whose scope covers it and which lists every
// required check.
func TestGateReceiptVerifyAcceptsATrustedReceipt(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	stdout, stderr, status := f.verify(f.head, f.headTree, "--require-check", "coverage-tiers")
	if status != 0 {
		t.Fatalf("verify = %d, want 0\n%s", status, stderr)
	}
	if !strings.Contains(stdout, "signature for gate@test") {
		t.Errorf("verify reported %q, want the signature's own description of the signer", stdout)
	}
	if !strings.Contains(stdout, "gate-receipt: accepted refs/gate/"+f.head) {
		t.Errorf("verify reported %q, want the ref it accepted", stdout)
	}
	// The checks it relied on are listed, one per line, in the receipt's own order.
	for _, want := range []string{"gate-receipt:   check gofmt", "gate-receipt:   check go-test-race"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("verify did not list %q:\n%s", want, stdout)
		}
	}
}

// TestGateReceiptVerifyAcceptsTheWholeSuite pins the list CI actually requires: a receipt
// that carries the policy's suite is accepted with `--require-suite full`, and one missing a
// member is refused. The list is asked of the policy rather than restated, so a gate step
// renamed in one place and not the other cannot turn into a skipped check.
func TestGateReceiptVerifyAcceptsTheWholeSuite(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	suite := policy.VerifySuite()
	if len(suite) == 0 {
		t.Fatal("the policy names no checks, so a full-scope receipt could never be accepted")
	}

	if _, stderr, status := f.create(f.head, "full", strings.Join(suite, " ")); status != 0 {
		t.Fatalf("create with the suite's own list = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.publish(f.head); status != 0 {
		t.Fatalf("publish = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.verify(f.head, f.headTree, "--require-suite", "full"); status != 0 {
		t.Fatalf("verify with the whole suite = %d, want 0\n%s", status, stderr)
	}

	// One member short is a refusal that names the member, because CI's fallback is only
	// trustworthy when it says which check it could not rely on.
	missing := suite[len(suite)-1]
	partial := make([]string, 0, len(suite)-1)
	for _, check := range suite {
		if check != missing {
			partial = append(partial, check)
		}
	}
	if _, stderr, status := f.create(f.head, "full", strings.Join(partial, " ")); status != 0 {
		t.Fatalf("create without %s = %d, want 0: %s", missing, status, stderr)
	}
	if _, stderr, status := f.publish(f.head); status != 0 {
		t.Fatalf("publish = %d, want 0: %s", status, stderr)
	}
	_, stderr, status := f.verify(f.head, f.headTree, "--require-suite", "full")
	if status != 1 {
		t.Fatalf("verify without %s = %d, want the refusal status 1\n%s", missing, status, stderr)
	}
	if !strings.Contains(stderr, "required check '"+missing+"'") {
		t.Errorf("the refusal %q does not name the missing member %s", stderr, missing)
	}
}

// TestGateReceiptVerifyAcceptsADocsReceiptForADocsChange pins the other arm of the scope
// rule: a documentation-only change may be excused by a documentation receipt, and the
// change is classified by the command rather than by what the receipt claims.
func TestGateReceiptVerifyAcceptsADocsReceiptForADocsChange(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	// The base is the commit the docs change was measured from, which for a docs-only
	// branch is its own parent rather than the merge base with origin/main.
	if _, stderr, status := f.run("create", "--sha", f.docs, "--base", f.head,
		"--scope", "docs", "--checks", "doc-integrity version-fields"); status != 0 {
		t.Fatalf("create for a docs-only change = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.publish(f.docs); status != 0 {
		t.Fatalf("publish = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.run("verify", "--sha", f.docs, "--tree", f.docsTree, "--base", f.head,
		"--signers", filepath.Join(f.work, "allow-trusted"), "--require-check", "doc-integrity"); status != 0 {
		t.Fatalf("verify for a docs-only change = %d, want 0\n%s", status, stderr)
	}
}

// ---------------------------------------------------------------------------
// verify: every refusal
// ---------------------------------------------------------------------------

// TestGateReceiptVerifyRefusesAnUntrustedSigner is the load-bearing one: the push authority
// that created the ref is not the trust decision. A receipt signed by a key outside the
// allow-list is refused however well formed it is.
func TestGateReceiptVerifyRefusesAnUntrustedSigner(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	// The same receipt, re-signed with the other key and republished: everything about it
	// is right except who made it.
	object := gateReceiptGit(t, f.repo, f.otherKeyEnv(), "commit-tree", "-S",
		"-m", f.receiptBody(f.head), f.headTree, "-p", f.head)
	f.setPublishedRef(f.head, object)

	_, stderr, status := f.verify(f.head, f.headTree)
	if status != 1 {
		t.Fatalf("verify of an untrusted signature = %d, want 1", status)
	}
	if !strings.Contains(stderr, "not signed by a key in") {
		t.Errorf("the refusal %q does not name the allow-list", stderr)
	}
}

// TestGateReceiptVerifyRefusesARewrittenUnsignedPayload pins that the message is not the
// evidence: editing the receipt's text without the key leaves an object nobody signed, even
// though its parent and tree are right.
func TestGateReceiptVerifyRefusesARewrittenUnsignedPayload(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	rewritten := strings.Replace(f.receiptBody(f.head), "scope full", "scope docs", 1)
	object := f.commitTree(rewritten, f.headTree, f.head, false)
	f.setPublishedRef(f.head, object)

	_, stderr, status := f.verify(f.head, f.headTree)
	if status != 1 {
		t.Fatalf("verify of a rewritten, unsigned receipt = %d, want 1", status)
	}
	if !strings.Contains(stderr, "not signed by a key in") {
		t.Errorf("the refusal %q does not name the signer", stderr)
	}
}

// TestGateReceiptVerifyRefusesAReceiptBuiltOnAnotherCommit pins the structural check that
// makes a receipt non-copyable: a well-signed receipt on a different parent is refused
// before its own text is read.
func TestGateReceiptVerifyRefusesAReceiptBuiltOnAnotherCommit(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	object := f.commitTree(f.receiptBody(f.head), f.headTree, f.base, true)
	f.setPublishedRef(f.head, object)

	_, stderr, status := f.verify(f.head, f.headTree)
	if status != 1 {
		t.Fatalf("verify of a receipt on another commit = %d, want 1", status)
	}
	if !strings.Contains(stderr, "is built on") {
		t.Errorf("the refusal %q does not name the parent it found", stderr)
	}
}

// TestGateReceiptVerifyRefusesADifferentTree pins the second structural check: a receipt
// that covers a tree CI did not check out is refused, because it gated something else.
func TestGateReceiptVerifyRefusesADifferentTree(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	otherTree := f.git("rev-parse", f.base+"^{tree}")
	_, stderr, status := f.verify(f.head, otherTree)
	if status != 1 {
		t.Fatalf("verify against another tree = %d, want 1", status)
	}
	if !strings.Contains(stderr, "was checked out") {
		t.Errorf("the refusal %q does not name the tree", stderr)
	}
}

// TestGateReceiptVerifyRefusesABaseOutsideThePullRequest pins the ancestry rule. The base is
// a root commit with nothing to do with this history, so only the ancestry check can refuse
// it.
func TestGateReceiptVerifyRefusesABaseOutsideThePullRequest(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	orphan := f.commitTree("unrelated base", f.git("rev-parse", f.base+"^{tree}"), "", false)
	_, stderr, status := f.run("verify", "--sha", f.head, "--tree", f.headTree, "--base", orphan,
		"--signers", filepath.Join(f.work, "allow-trusted"))
	if status != 1 {
		t.Fatalf("verify with an unrelated pull request base = %d, want 1\n%s", status, stderr)
	}
	if !strings.Contains(stderr, "not an ancestor of the pull request base") {
		t.Errorf("the refusal %q does not name the pull request base", stderr)
	}
}

// TestGateReceiptVerifyRefusesAForgedDiffHash pins that the change is re-measured rather
// than believed: a correctly signed receipt whose diff line names another change is refused.
func TestGateReceiptVerifyRefusesAForgedDiffHash(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	var lines []string
	for _, line := range strings.Split(f.receiptBody(f.head), "\n") {
		if strings.HasPrefix(line, "diff ") {
			line = "diff 0000000000000000000000000000000000000000"
		}
		lines = append(lines, line)
	}
	object := f.commitTree(strings.Join(lines, "\n"), f.headTree, f.head, true)
	f.setPublishedRef(f.head, object)

	_, stderr, status := f.verify(f.head, f.headTree)
	if status != 1 {
		t.Fatalf("verify of a forged diff hash = %d, want 1", status)
	}
	if !strings.Contains(stderr, "hashes the change as") {
		t.Errorf("the refusal %q does not name the diff hash", stderr)
	}
}

// TestGateReceiptVerifyRefusesADocsReceiptForAGoChange pins that the scope is recomputed: a
// documentation-scope receipt cannot excuse a change that touches Go, or the narrow gate
// would be a way past the whole suite.
func TestGateReceiptVerifyRefusesADocsReceiptForAGoChange(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	if _, stderr, status := f.create(f.head, "docs", "doc-integrity"); status != 0 {
		t.Fatalf("create for a docs-scope receipt = %d, want 0: %s", status, stderr)
	}
	if _, stderr, status := f.publish(f.head); status != 0 {
		t.Fatalf("publish = %d, want 0: %s", status, stderr)
	}
	_, stderr, status := f.verify(f.head, f.headTree)
	if status != 1 {
		t.Fatalf("verify of a docs receipt for a Go change = %d, want 1", status)
	}
	if !strings.Contains(stderr, "does not cover the full change") {
		t.Errorf("the refusal %q does not name the scope", stderr)
	}
}

// TestGateReceiptVerifyRefusesAMissingRequiredCheck pins the check list: a receipt that does
// not list a required check is refused by name.
func TestGateReceiptVerifyRefusesAMissingRequiredCheck(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	_, stderr, status := f.verify(f.head, f.headTree, "--require-check", "no-such-check")
	if status != 1 {
		t.Fatalf("verify with a missing required check = %d, want 1", status)
	}
	if !strings.Contains(stderr, "does not list the required check 'no-such-check'") {
		t.Errorf("the refusal %q does not name the check", stderr)
	}
}

// TestGateReceiptVerifyRefusesWhenThereIsNoReceipt pins the absence case: no ref at all is a
// refusal, and CI runs the whole gate. The entry is asked about a commit whose receipt was
// never published.
func TestGateReceiptVerifyRefusesWhenThereIsNoReceipt(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	_, stderr, status := f.run("verify", "--sha", f.base, "--tree", f.headTree,
		"--signers", filepath.Join(f.work, "allow-trusted"))
	if status != 1 {
		t.Fatalf("verify with no receipt = %d, want 1", status)
	}
	if !strings.Contains(stderr, "no gate receipt at") {
		t.Errorf("the refusal %q does not name the ref", stderr)
	}
}

// TestGateReceiptVerifyRefusesAMissingAllowList pins that a verification without a trust
// anchor refuses rather than trusting whatever key the runner happens to know.
func TestGateReceiptVerifyRefusesAMissingAllowList(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	_, stderr, status := f.run("verify", "--sha", f.head, "--tree", f.headTree,
		"--signers", filepath.Join(f.work, "no-such-allow-list"))
	if status != 1 {
		t.Fatalf("verify with no allow-list = %d, want 1", status)
	}
	if !strings.Contains(stderr, "signer allow-list not found") {
		t.Errorf("the refusal %q does not name the allow-list", stderr)
	}
}

// ---------------------------------------------------------------------------
// show and suite
// ---------------------------------------------------------------------------

// TestGateReceiptShowPrintsTheLocalReceiptThenThePublishedOne pins show's two sources and
// the order they are tried in: this clone's own file first, because that is the run a person
// is usually inspecting, and the published receipt when there is no local file.
func TestGateReceiptShowPrintsTheLocalReceiptThenThePublishedOne(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)
	f.writeReceipt()

	stdout, stderr, status := f.run("show", "--sha", f.head)
	if status != 0 {
		t.Fatalf("show = %d, want 0: %s", status, stderr)
	}
	// The file is printed verbatim, so the local source is recognisable as the file the
	// gate's own step wrote.
	if stdout != f.receipt(f.head) {
		t.Errorf("show printed %q, want the local receipt file verbatim", stdout)
	}
	if _, err := receipt.Parse(stdout); err != nil {
		t.Errorf("show printed something that does not parse: %v", err)
	}
	// The published source prints the commit's message, which has no trailing newline; the
	// local file's text is what that message was built from.
	publishedForm := strings.TrimSuffix(stdout, "\n")
	// With the local file gone, the published receipt is what remains — the case a
	// reviewer's clone is in.
	common := f.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err := os.Remove(filepath.Join(common, "gate-receipts", f.head+".receipt")); err != nil {
		t.Fatalf("removing the local receipt: %v", err)
	}
	stdout, stderr, status = f.run("show", "--sha", f.head)
	if status != 0 {
		t.Fatalf("show of a published receipt = %d, want 0\n%s", status, stderr)
	}
	// The published form is the commit's message: the local file's text, which is what that
	// message was built from. Both sources are checked to agree rather than to have a
	// particular trailing byte, because the newline a file ends with is not part of a
	// commit's message.
	if strings.TrimSuffix(stdout, "\n") != strings.TrimSuffix(publishedForm, "\n") {
		t.Errorf("show printed the published receipt as %q (len %d, want len %d)", stdout, len(stdout), len(publishedForm))
	}
	if !strings.Contains(stdout, "commit "+f.head) {
		t.Errorf("the published receipt show printed does not name %s:\n%s", f.head, stdout)
	}
	if _, err := receipt.Parse(stdout); err != nil {
		t.Errorf("show printed the published receipt but it does not parse: %v", err)
	}

	// A commit with neither is a refusal.
	_, stderr, status = f.run("show", "--sha", f.base)
	if status != 1 {
		t.Fatalf("show of a commit with no receipt = %d, want 1\n%s", status, stderr)
	}
	if !strings.Contains(stderr, "no receipt for") {
		t.Errorf("the refusal %q does not name the commit", stderr)
	}
}

// TestGateReceiptSuitePrintsTheChecksAFullReceiptMustList pins the suite: it is the policy's
// list, in the policy's order, one per line, because CI reads it as the set a receipt must
// carry before it may skip its own run.
func TestGateReceiptSuitePrintsTheChecksAFullReceiptMustList(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if err := runGateReceipt([]string{"suite"}, &out, &errOut); err != nil {
		t.Fatalf("suite: %v\n%s", err, errOut.String())
	}
	got := splitLines(out.String())
	want := policy.VerifySuite()
	if len(got) != len(want) {
		t.Fatalf("suite printed %d checks, want %d:\n%s", len(got), len(want), out.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suite printed %q at line %d, want %q", got[i], i+1, want[i])
		}
		// Every name it prints must be one the line-oriented format can carry, or the
		// receipt CI is asked to require could never list it.
		if err := receipt.CheckName(got[i]); err != nil {
			t.Errorf("suite printed %q, which the receipt format cannot carry: %v", got[i], err)
		}
	}
}

// ---------------------------------------------------------------------------
// usage
// ---------------------------------------------------------------------------

// TestGateReceiptUsage pins the invocation mistakes. Each is a value a caller passes, so the
// case is a table: the status is 2 for every one of them, which is what tells a script "you
// called me wrong" rather than "the evidence is unusable".
func TestGateReceiptUsage(t *testing.T) {
	t.Parallel()
	f := newGateReceiptFixture(t)

	cases := []struct {
		name string
		args []string
	}{
		{name: "no command", args: nil},
		{name: "an unknown command", args: []string{"sideways"}},
		{name: "verify without --sha", args: []string{"verify", "--tree", f.headTree}},
		{name: "verify without --tree", args: []string{"verify", "--sha", f.head}},
		{name: "verify with an unknown suite", args: []string{"verify", "--sha", f.head, "--tree", f.headTree,
			"--require-suite", "partial"}},
		{name: "create without --scope", args: []string{"create", "--sha", f.head, "--checks", "gofmt"}},
		{name: "create with a stray argument", args: []string{"create", "--scope", "full", "extra"}},
		{name: "create with an unknown option", args: []string{"create", "--scope", "full", "--sideways"}},
		{name: "suite with an argument", args: []string{"suite", "extra"}},
		{name: "show with an unknown option", args: []string{"show", "--sideways"}},
		{name: "publish with a commit that does not exist", args: []string{"publish", "--sha", "no-such-revision"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, stderr, status := f.run(tc.args...)
			if status != 2 {
				t.Fatalf("%s = %d, want the usage status 2\n%s", tc.name, status, stderr)
			}
			if stderr == "" {
				t.Errorf("%s said nothing on standard error", tc.name)
			}
		})
	}
}
