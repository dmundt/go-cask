package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/receipt"
)

// gateReceiptRefPrefix is the namespace the receipt travels under. It is a ref and not a
// branch, so nothing is ever committed to it in the ordinary sense: deleting the ref
// deletes the record, and the record cannot be mistaken for work.
const gateReceiptRefPrefix = "refs/gate"

// gateSignersPath is the repository's signer allow-list, relative to the root. The
// allow-list has to be a repository file rather than a runner setting: an SSH signature
// means nothing until the keys this repository trusts decide whether it counts, or a
// verification would accept whatever key the runner happened to know.
const gateSignersPath = ".github/gate-signers"

// gateSignersEnv overrides the allow-list. A runner that checks out a pull request and keeps
// its trusted keys somewhere else points this at them, and the behaviour tests use it to
// sign with throwaway keys.
const gateSignersEnv = "GATE_RECEIPT_SIGNERS"

// gateScopeCmdEnv overrides the command that decides whether a change is documentation
// only. The decision is asked IN the repository being verified, because that repository's
// change is what it classifies, and a repository that is not this module cannot answer with
// this module's command.
const gateScopeCmdEnv = "GATE_RECEIPT_SCOPE_CMD"

// gateScopeRule is the change-set rule the scope recomputation asks for.
const gateScopeRule = "docs_only"

// gateScopeRuleFunc asks whether the change from base to sha is documentation only.
//
// It is a collaborator rather than a direct call because the classification runs a second
// program, and a test that had to spawn that program would be testing this module's build as
// much as the receipt. The command is still the production answer — a test replaces the
// question, never the answer.
type gateScopeRuleFunc func(root, base, sha string) (bool, error)

// gateReceiptDeps are the receipt command's collaborators.
type gateReceiptDeps struct {
	// scopeDocs reports whether the change a receipt covers is documentation only.
	scopeDocs gateScopeRuleFunc
}

// productionGateReceiptDeps returns the collaborators the command uses outside a test.
func productionGateReceiptDeps() gateReceiptDeps {
	return gateReceiptDeps{scopeDocs: gateReceiptDocsOnly}
}

// gateReceiptUsage is the command's own help, in the helper's shape: the verbs first, then
// what each one takes.
const gateReceiptUsage = `usage: buildtool gate-receipt <command>

  create  --scope docs|full [--sha SHA] [--base SHA] [--checks "a b c"]
          [--coverage-tiers N]        write the receipt for a green gate run
  publish [--sha SHA] [--remote NAME] [--quiet]
                                      sign it and push refs/gate/<sha>
  verify  --sha SHA --tree TREE [--base SHA] [--require-check NAME]...
          [--require-suite full] [--signers FILE]
                                      accept the receipt, or exit 1
  show    [--sha SHA]                 print the receipt for a commit
  suite                               print the checks a full receipt must list
`

// runGateReceipt dispatches the gate receipt's verbs.
//
// The receipt is the transport for the local gate's verdict: the gate stamp lives in the
// shared git directory, which GitHub cannot read, so a green local run would be repeated in
// CI unless the evidence travelled. It travels as a signed commit under `refs/gate/<sha>`
// whose parent is the gated commit, whose tree is the gated tree and whose message is the
// record — so a receipt carries its own signature and CI can check two structural claims
// without reading the text at all. The record's own format belongs to
// internal/build/receipt; what is here is git: signing the object, pushing it, and
// deciding what a receipt is allowed to excuse.
func runGateReceipt(args []string, out, errOut io.Writer) error {
	// The repository is resolved once, from the process's own directory, because that is
	// where the caller put the command: the gate's step starts it in the worktree being
	// verified and the pre-push hook starts it in the checkout being pushed. A caller with a
	// different repository to speak about passes it to the command below instead.
	root, err := repoRoot()
	if err != nil {
		return err
	}
	return gateReceiptCommand(root, args, out, errOut, productionGateReceiptDeps())
}

// gateReceiptCommand is the command with its collaborators injected, in the repository it
// speaks about.
func gateReceiptCommand(root string, args []string, out, errOut io.Writer, deps gateReceiptDeps) error {
	if len(args) == 0 {
		return misuse(errOut, gateReceiptUsage)
	}
	switch args[0] {
	case "create":
		return runGateReceiptCreate(root, args[1:], out, errOut)
	case "publish":
		return runGateReceiptPublish(root, args[1:], out, errOut)
	case "verify":
		return runGateReceiptVerify(root, args[1:], out, errOut, deps)
	case "show":
		return runGateReceiptShow(root, args[1:], out, errOut)
	case "suite":
		if len(args) > 1 {
			return misuse(errOut, "suite takes no arguments")
		}
		for _, name := range policy.VerifySuite() {
			fmt.Fprintln(out, name)
		}
		return nil
	case "-h", "--help", "help":
		fmt.Fprint(out, gateReceiptUsage)
		return nil
	default:
		return misuse(errOut, "unknown command '%s'", args[0])
	}
}

// refuse prints a refusal the way the helper did — the command's name and the reason — and
// returns a status of its own with no message, because the message has already been written
// and a caller that printed it twice would look like two failures.
//
// A refusal is never a usage mistake: CI treats a refused receipt as "there is no usable
// evidence" and runs the whole gate, which is the safe direction, while a usage error is a
// caller that cannot be understood at all.
func refuse(errOut io.Writer, format string, args ...any) error {
	fmt.Fprintf(errOut, "gate-receipt: %s\n", fmt.Sprintf(format, args...))
	return exitStatus(1)
}

// misuse reports an invocation mistake the way the helper's `die` did: the text on standard
// error, and a status that tells a script the caller was wrong rather than that the evidence
// was unusable. It is a separate report from refuse because the two statuses are the whole
// difference between "run the full gate" and "fix your command line".
func misuse(errOut io.Writer, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(errOut, "gate-receipt: %s\n", message)
	return usageError{message}
}

// runGateReceiptCreate writes the receipt for a green run and prints its path, which is the
// one line the gate's step reports.
func runGateReceiptCreate(root string, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("gate-receipt create", flag.ContinueOnError)
	flags.SetOutput(errOut)
	scope := flags.String("scope", "", "what the run covered: docs or full (required)")
	sha := flags.String("sha", "", "the commit the run verified; omitted, HEAD")
	base := flags.String("base", "", "the merge base the change was measured from; omitted, origin/main")
	checks := flags.String("checks", os.Getenv("GATE_RECEIPT_CHECKS"), "the checks the run completed, space separated")
	tiers := flags.String("coverage-tiers", "", "how many packages the coverage gate covered")
	if err := parse(flags, args); err != nil {
		return usageError{err.Error()}
	}

	options, err := gateReceiptCreateOptions(root, *scope, *sha, *base, *checks, *tiers)
	if err != nil {
		return usageError{err.Error()}
	}
	path, err := CreateGateReceipt(root, options)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

// GateReceiptOptions is a receipt to write: what a green run covered, and by what.
type GateReceiptOptions struct {
	// Scope is what the run covered, as the receipt's own tokens spell it.
	Scope receipt.Scope
	// SHA is the commit the run verified. Empty means HEAD.
	SHA string
	// Base is the merge base the change was measured from. Empty means the merge base
	// with origin/main, which is the base the gate's own scope decision used.
	Base string
	// Checks are the checks the run completed, in the order it recorded them.
	Checks []string
	// CoverageTiers is how many packages the coverage gate covered, empty when the run
	// measured nothing.
	CoverageTiers string
}

// gateReceiptCreateOptions resolves the caller's words into the facts a receipt records:
// the commit, the base, and the tree and diff hash derived from them. Every failure here is
// the caller's mistake rather than a broken rule, which is why the command turns them into
// a usage error; the messages are the helper's, because a person debugging a failed gate
// run is reading them.
func gateReceiptCreateOptions(root string, scope, sha, base, checks, tiers string) (GateReceiptOptions, error) {
	options := GateReceiptOptions{CoverageTiers: tiers}

	parsed, err := receipt.ParseScope(scope)
	if err != nil {
		return options, fmt.Errorf("create: --scope must be docs or full (got '%s')", scope)
	}
	options.Scope = parsed

	// The checks are validated before anything is resolved, so a receipt with an
	// unreadable check name is refused rather than written and refused later: a name that
	// cannot ride in the line-oriented format makes the record unparseable, and an
	// unparseable receipt is refused by every verification, which is a silent kind of
	// wrong. An empty name cannot occur — the split drops empty fields — so a name this
	// rejects is one that really does carry a space, a newline or punctuation.
	for _, check := range strings.Fields(checks) {
		if err := receipt.CheckName(check); err != nil {
			return options, fmt.Errorf("create: %w", err)
		}
	}
	options.Checks = strings.Fields(checks)

	if sha == "" {
		sha = "HEAD"
	}
	resolved, err := gitPathIn(root, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return options, fmt.Errorf("create: not a commit: %s", sha)
	}
	options.SHA = resolved

	// The base is the base the scope decision used, so a caller that measured the change
	// passes the one it measured; a caller without one gets origin/main, and a clone with
	// no remote at all has no base to measure from and must say so.
	if base == "" {
		mergeBase, err := gitOutputIn(root, "merge-base", "origin/main", options.SHA)
		if err != nil || strings.TrimSpace(mergeBase) == "" {
			return options, errors.New("create: no merge base with origin/main; pass --base")
		}
		base = strings.TrimSpace(mergeBase)
	}
	resolvedBase, err := gitPathIn(root, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return options, fmt.Errorf("create: base is not a commit: %s", base)
	}
	options.Base = resolvedBase
	return options, nil
}

// CreateGateReceipt writes the receipt for a green run and returns its path.
//
// It signs nothing and touches no network: the record is a local file, and publishing it is
// the separate, best-effort step a push performs. The file is written through a temporary
// name in the same directory and renamed into place, because a receipt half written by an
// interrupted run must never be the file a publish picks up — the same atomicity the gate
// stamp has, and for the same reason.
func CreateGateReceipt(root string, options GateReceiptOptions) (string, error) {
	if _, err := receipt.ParseScope(string(options.Scope)); err != nil {
		return "", err
	}
	sha, err := gitPathIn(root, "rev-parse", "--verify", options.SHA+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("create: not a commit: %s", options.SHA)
	}
	tree, err := gitPathIn(root, "rev-parse", sha+"^{tree}")
	if err != nil {
		return "", err
	}
	base, err := gitPathIn(root, "rev-parse", "--verify", options.Base+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("create: base is not a commit: %s", options.Base)
	}
	diff, err := gateReceiptPathHash(root, base, sha)
	if err != nil {
		return "", err
	}

	record := receipt.Record{
		Commit:        sha,
		Tree:          tree,
		Base:          base,
		Diff:          diff,
		Scope:         options.Scope,
		Checks:        options.Checks,
		CoverageTiers: options.CoverageTiers,
		Go:            goToolchainVersion(),
		Runner:        gateReceiptRunner(),
		Run:           time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	return writeGateReceipt(root, record)
}

// goToolchainVersion is the `go` field: the version the gate ran on, or `unknown` when the
// toolchain cannot be asked. A receipt with no version is less useful but not wrong, and
// refusing to write one would trade a missing detail for a missing receipt.
func goToolchainVersion() string {
	// The version is derived rather than reported by `go version`, which would cost a
	// process per receipt and print a target triple the record does not want. The
	// toolchain that built this command is the toolchain that ran the gate.
	if version := runtime.Version(); version != "" {
		return version
	}
	return "unknown"
}

// gateReceiptRunner names the machine the run happened on, in the helper's own shape:
// the lowercased OS and the architecture. It is part of the record and deliberately not
// part of a receipt's identity, because two runs of one commit on two hosts are the same
// evidence.
func gateReceiptRunner() string {
	return strings.ToLower(runtime.GOOS) + "/" + runtime.GOARCH
}

// writeGateReceipt writes one record to the shared git directory's receipt directory and
// returns its path.
//
// The directory is the git COMMON directory, not this worktree's: every worktree of a clone
// runs its own gate, and the receipts all belong to the clone, so a publish from any of them
// finds the run that produced the evidence. The commit names the file, so the write is
// idempotent and two runs of one commit replace each other rather than accumulating.
func writeGateReceipt(root string, record receipt.Record) (string, error) {
	common, err := gitPathIn(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(common, "gate-receipts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, record.Commit+".receipt")

	// The temporary name carries the commit and a random suffix so two runs of one commit
	// — which is the ordinary case, since a republish follows a re-run — cannot collide on
	// the way to the rename.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("naming the temporary receipt: %w", err)
	}
	temp := filepath.Join(dir, "."+record.Commit+"."+hex.EncodeToString(suffix))

	if err := os.WriteFile(temp, []byte(receipt.Render(record)), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		// A rename that did not happen leaves nothing worth keeping: the next run writes
		// its own temporary file, and an orphan here would be a file the sweep cannot
		// explain.
		_ = os.Remove(temp)
		return "", fmt.Errorf("moving %s into place: %w", temp, err)
	}
	return path, nil
}

// gateReceiptPathHash hashes a change the way both sides of a verification do: the sorted
// list of changed paths, hashed with the repository's own object format.
//
// The hash is git's rather than this command's, because the object format belongs to the
// repository — a store may be sha256, and a receipt hashed with the wrong function would be
// refused by a verification that recomputed it correctly. Both sides hand git the same
// bytes, which is the half of the comparison the record package owns.
func gateReceiptPathHash(root, base, sha string) (string, error) {
	listed, err := gitOutputIn(root, "diff", "--name-only", base, sha)
	if err != nil {
		return "", err
	}
	return gitInput(root, receipt.CanonicalPaths(splitLines(listed)), "hash-object", "--stdin")
}

// runGateReceiptPublish signs the local receipt as a commit object and pushes it to the
// coordination ref CI reads.
func runGateReceiptPublish(root string, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("gate-receipt publish", flag.ContinueOnError)
	flags.SetOutput(errOut)
	sha := flags.String("sha", "", "the commit whose receipt is published; omitted, HEAD")
	remote := flags.String("remote", "origin", "the remote the receipt is pushed to")
	quiet := flags.Bool("quiet", false, "report nothing on success")
	if err := parse(flags, args); err != nil {
		return usageError{err.Error()}
	}

	target := *sha
	if target == "" {
		target = "HEAD"
	}
	resolved, err := gitPathIn(root, "rev-parse", "--verify", target+"^{commit}")
	if err != nil {
		return usageError{fmt.Sprintf("publish: not a commit: %s", target)}
	}
	if err := publishGateReceipt(root, *remote, resolved, *quiet, out, errOut); err != nil {
		return err
	}
	return nil
}

// publishGateReceipt publishes one commit's receipt, if this clone has produced one.
//
// The object is a commit rather than an annotated tag for a mechanical reason: this ref
// namespace refuses a tag object on the remote, and the API route that could create one
// server-side cannot carry the developer's signature at all. A signed commit is pushable,
// verifiable with `git verify-commit`, and its shape gives CI two claims that do not depend
// on the receipt's text — its parent must BE the commit CI is testing, and its tree must BE
// the tree CI checked out.
//
// The signing is done by git, so the key, its format and the principal are whatever this
// toolchain signs commits with. A toolchain without SSH signing configured cannot publish,
// and that is a refusal rather than a fallback: there is nothing this command could sign
// with, and CI running the whole gate is the safe direction.
func publishGateReceipt(repo, remote, sha string, quiet bool, out, errOut io.Writer) error {
	payload, err := gateReceiptLocal(repo, sha)
	if err != nil {
		return refuse(errOut, "no local gate receipt for %s — run ./scripts/verify.sh, then publish", sha)
	}
	if _, err := receipt.Parse(payload); err != nil {
		return refuse(errOut, "the receipt for %s is malformed; run ./scripts/verify.sh again", sha)
	}

	format, err := gitOutputIn(repo, "config", "--get", "gpg.format")
	if err != nil || strings.TrimSpace(format) != "ssh" {
		return refuse(errOut, "gate receipts are signed with an SSH key; set 'git config gpg.format ssh' "+
			"and user.signingkey, or publish from the toolchain that has them")
	}

	ref := gateReceiptRefPrefix + "/" + sha
	tree, err := gitPathIn(repo, "rev-parse", sha+"^{tree}")
	if err != nil {
		return err
	}
	// `git commit-tree -S` signs through the same identity the branch's commits carry, so
	// the receipt is evidence of the same hand that made the commit. The parent is the
	// gated commit and the tree is its tree, which is what CI checks before it reads a
	// single field of the message.
	object, signed := gateReceiptSign(repo, sha, tree, payload)
	if !signed {
		return refuse(errOut, "cannot sign in this toolchain (git commit-tree -S failed); configure "+
			"gpg.format, user.signingkey, user.name and user.email")
	}
	if err := gitRun(repo, "update-ref", ref, object); err != nil {
		return err
	}

	published := gateReceiptRemoteRef(repo, remote, ref)
	switch {
	case published == object:
		gateReceiptNote(quiet, out, "gate-receipt: already published: %s", ref)
		return nil
	case published != "":
		// A second run of the gate for one commit writes different bytes — the run time
		// differs — but the same evidence. The ref is replaced only when the evidence
		// itself moved, and the replacement is announced: silently rewriting what CI reads
		// is the one thing this ref must never do.
		//
		// The lease is the object this clone just read from the remote, so a ref another
		// publisher moved in between is not overwritten from under them.
		if gateReceiptSameEvidence(repo, remote, ref, published, payload) {
			gateReceiptNote(quiet, out, "gate-receipt: already published (same evidence): %s", ref)
			return nil
		}
		if err := gitRun(repo, "push", "--no-verify", "--quiet",
			"--force-with-lease="+ref+":"+published, remote, ref+":"+ref); err != nil {
			return refuse(errOut, "could not replace %s on %s", ref, remote)
		}
		gateReceiptNote(quiet, out, "gate-receipt: replaced the receipt at %s with this run", ref)
		return nil
	default:
		if err := gitRun(repo, "push", "--no-verify", "--quiet", remote, ref+":"+ref); err != nil {
			return refuse(errOut, "could not push %s to %s", ref, remote)
		}
		gateReceiptNote(quiet, out, "gate-receipt: published %s", ref)
		return nil
	}
}

// gateReceiptNote reports one publish outcome unless the caller asked for silence, which is
// what a push wants: the pre-push hook publishes on every push, and a hook that narrated
// every successful push would be noise in every landing.
func gateReceiptNote(quiet bool, out io.Writer, format string, args ...any) {
	if quiet {
		return
	}
	fmt.Fprintf(out, format+"\n", args...)
}

// gateReceiptLocal reads this clone's receipt file for a commit. A missing file is a
// refusal and not an error: a commit nobody ran the gate for has no evidence, which is
// exactly the state CI's fallback exists for.
func gateReceiptLocal(repo, sha string) (string, error) {
	common, err := gitPathIn(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(filepath.Join(common, "gate-receipts", sha+".receipt"))
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// gateReceiptSign builds the receipt commit with git, so the signature is the toolchain's
// own. It reports whether the signature was made separately from any git error, because a
// toolchain that cannot sign at all is a refusal with its own remedy rather than a failure
// of the receipt.
//
// The message is handed over without its trailing newline. Git strips trailing whitespace
// from a message it is given, so a payload that reached it terminated would be signed over
// text that differs from the text a reader gets back — and the signature verifies the
// message, so such a receipt would fail verification while looking perfectly well signed.
func gateReceiptSign(repo, parent, tree, payload string) (string, bool) {
	message := strings.TrimSuffix(payload, "\n")
	args := gateReceiptGitArgs(repo, "commit-tree", "-S", "-p", parent, "-m", message, tree)
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", false
	}
	object := strings.TrimSpace(stdout.String())
	return object, object != ""
}

// gateReceiptRemoteRef is the object the remote's receipt ref points at, or the empty
// string when the ref does not exist there. A remote that cannot be reached reads as "no
// published receipt", which the publish path turns into a push rather than a refusal: git
// reports the push's own failure with far more of the reason than a probe would.
func gateReceiptRemoteRef(repo, remote, ref string) string {
	listed, err := gitOutputIn(repo, "ls-remote", "--exit-code", remote, ref)
	if err != nil {
		return ""
	}
	for _, line := range splitLines(listed) {
		if fields := strings.Fields(line); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// gateReceiptSameEvidence reports whether the published receipt covers the same thing as
// the local one, whatever the two runs' own times and hosts were.
//
// The published receipt is read from the remote's ref rather than assumed: a receipt that
// another clone replaced with different evidence is not the one this publish would be
// skipping, and comparing against a cached answer would let this ref keep a record nobody
// verified.
func gateReceiptSameEvidence(repo, remote, ref, published, payload string) bool {
	if err := gitRun(repo, "fetch", "--no-tags", "--quiet", remote, ref); err != nil {
		return false
	}
	fetched, err := gitPathIn(repo, "rev-parse", "--verify", "FETCH_HEAD")
	if err != nil {
		return false
	}
	// The remote's ref may have moved between the probe and the fetch, which is what the
	// object comparison catches: a different object is a different receipt, and the
	// difference is decided by re-probing rather than by trusting the earlier answer.
	if fetched != published {
		return false
	}
	body, err := gitOutputIn(repo, "cat-file", "commit", fetched)
	if err != nil {
		return false
	}
	publishedRecord, err := receipt.Parse(gateReceiptBody(body))
	if err != nil {
		return false
	}
	local, err := receipt.Parse(payload)
	if err != nil {
		return false
	}
	return publishedRecord.Identity() == local.Identity()
}

// runGateReceiptVerify is the CI side: it accepts a receipt, or refuses it and leaves the
// caller to run the whole gate.
//
// Every refusal is a fallback in CI and never a false accept, so the order the checks run
// in is the order a person debugging a rejected receipt reads them: the ref exists, the
// object is a receipt commit built on this commit and carrying this tree, the signature is
// from a trusted key, then the record's own claims — each recomputed rather than believed.
func runGateReceiptVerify(root string, args []string, out, errOut io.Writer, deps gateReceiptDeps) error {
	flags := flag.NewFlagSet("gate-receipt verify", flag.ContinueOnError)
	flags.SetOutput(errOut)
	sha := flags.String("sha", "", "the commit CI is testing (required)")
	tree := flags.String("tree", "", "the tree CI checked out (required)")
	base := flags.String("base", "", "the pull request base the receipt's base must be an ancestor of")
	ref := flags.String("ref", "", "the ref the receipt lives at; default refs/gate/<sha>")
	signers := flags.String("signers", "", "the signer allow-list; default .github/gate-signers")
	var require stringList
	flags.Var(&require, "require-check", "a check the receipt must list; repeatable")
	suite := flags.String("require-suite", "", "require a whole suite: full")
	if err := parse(flags, args); err != nil {
		return usageError{err.Error()}
	}
	if *sha == "" {
		return usageError{"verify: --sha is required"}
	}
	if *tree == "" {
		return usageError{"verify: --tree is required (the tree that was checked out)"}
	}
	// The suite is expanded from the policy table rather than from a list kept here: a
	// step renamed in the gate and not in this list would turn into a skipped check in
	// CI, which is the one failure mode of the whole mechanism.
	if *suite != "" {
		if *suite != "full" {
			return usageError{"verify: --require-suite knows only 'full'"}
		}
		require = append(require, policy.VerifySuite()...)
	}
	if *ref == "" {
		*ref = gateReceiptRefPrefix + "/" + *sha
	}
	if *signers == "" {
		*signers = gateReceiptSigners(root)
	}

	if _, err := gitPathIn(root, "rev-parse", "--verify", "--quiet", *ref); err != nil {
		return refuse(errOut, "no gate receipt at %s", *ref)
	}
	kind, err := gitPathIn(root, "cat-file", "-t", *ref)
	if err != nil {
		return err
	}
	if kind != "commit" {
		return refuse(errOut, "%s is not a receipt commit", *ref)
	}

	// Shape before text: the receipt commit must hang off the commit CI is asking about
	// and carry the tree CI checked out. Both are structural, so neither depends on what
	// the receipt claims about itself.
	parent, err := gitPathIn(root, "rev-parse", "--verify", "--quiet", *ref+"^")
	if err != nil {
		parent = ""
	}
	if parent != *sha {
		return refuse(errOut, "the receipt at %s is built on %s, not on commit %s",
			*ref, gateReceiptOrNothing(parent), *sha)
	}
	builtTree, err := gitPathIn(root, "rev-parse", *ref+"^{tree}")
	if err != nil {
		return err
	}
	if builtTree != *tree {
		return refuse(errOut, "the receipt at %s carries tree %s, but %s was checked out", *ref, builtTree, *tree)
	}

	// The signature is what makes the receipt evidence rather than a claim, and the
	// allow-list is what makes the signature mean a key this repository trusts. The
	// push authority that created the ref is deliberately not the question: a receipt
	// signed by a key outside the list is refused however it arrived here.
	if _, err := os.Stat(*signers); err != nil {
		return refuse(errOut, "signer allow-list not found: %s", *signers)
	}
	signature, err := gateReceiptVerifySignature(root, *ref, *signers)
	if err != nil {
		return refuse(errOut, "the receipt at %s is not signed by a key in %s", *ref, *signers)
	}

	body, err := gitOutputIn(root, "cat-file", "commit", *ref)
	if err != nil {
		return err
	}
	payload := gateReceiptBody(body)
	record, err := receipt.Parse(payload)
	if err != nil {
		return refuse(errOut, "the receipt at %s is not a %s", *ref, receipt.Version)
	}

	if record.Commit != *sha {
		return refuse(errOut, "the receipt names commit %s, not %s", record.Commit, *sha)
	}
	if record.Tree != *tree {
		return refuse(errOut, "the receipt covers tree %s, but %s was checked out", record.Tree, *tree)
	}
	if record.Base == "" {
		return refuse(errOut, "the receipt names no base")
	}
	if _, err := gitOutputIn(root, "merge-base", "--is-ancestor", record.Base, *sha); err != nil {
		return refuse(errOut, "the receipt base %s is not an ancestor of %s", record.Base, *sha)
	}
	if *base != "" {
		if _, err := gitOutputIn(root, "merge-base", "--is-ancestor", record.Base, *base); err != nil {
			return refuse(errOut, "the receipt base %s is not an ancestor of the pull request base %s",
				record.Base, *base)
		}
	}

	// The change is re-measured from the base the receipt names, in the repository being
	// verified, so a receipt for a change that has since moved does not describe the
	// change CI has.
	wantDiff, err := gateReceiptPathHash(root, record.Base, *sha)
	if err != nil {
		return err
	}
	if record.Diff != wantDiff {
		return refuse(errOut, "the receipt hashes the change as %s, but %s..%s is %s",
			record.Diff, record.Base, *sha, wantDiff)
	}

	// The scope is recomputed, never taken from the receipt: a receipt may only excuse
	// what it actually covered, or a documentation-scope gate would excuse a Go diff.
	// The rule is asked IN the repository being verified, because that repository's change
	// is what it classifies.
	wantScope := receipt.Full
	docsOnly, err := deps.scopeDocs(root, record.Base, *sha)
	if err != nil {
		return err
	}
	if docsOnly {
		wantScope = receipt.Docs
	}
	if _, err := receipt.ParseScope(string(record.Scope)); err != nil {
		return refuse(errOut, "the receipt scope '%s' is neither docs nor full", record.Scope)
	}
	if record.Scope != wantScope {
		return refuse(errOut, "a %s receipt does not cover the %s change %s..%s",
			record.Scope, wantScope, record.Base, *sha)
	}

	listed := make(map[string]bool, len(record.Checks))
	for _, check := range record.Checks {
		listed[check] = true
	}
	for _, check := range require {
		if !listed[check] {
			return refuse(errOut, "the receipt does not list the required check '%s'", check)
		}
	}

	fmt.Fprintf(out, "gate-receipt: accepted %s\n", *ref)
	fmt.Fprintf(out, "gate-receipt: %s\n", signature)
	fmt.Fprintf(out, "gate-receipt: scope %s, %d checks, gate ran %s on %s with %s\n",
		record.Scope, len(record.Checks), record.Run, record.Runner, record.Go)
	for _, check := range record.Checks {
		fmt.Fprintf(out, "gate-receipt:   check %s\n", check)
	}
	return nil
}

// gateReceiptSigners resolves the allow-list, letting the environment override the
// repository file so a test can sign with throwaway keys.
func gateReceiptSigners(root string) string {
	if override := os.Getenv(gateSignersEnv); override != "" {
		return override
	}
	return filepath.Join(root, filepath.FromSlash(gateSignersPath))
}

// gateReceiptVerifySignature checks the receipt commit's SSH signature against the
// allow-list and returns the line git printed about it, which the caller reports: the
// verification a person reads is git's own description of the key, not this command's
// paraphrase of it.
//
// `--raw` is passed because the signature status is a fact about the object and not a
// decoration of the message: a receipt whose message happens to contain a "Good signature"
// line must not be able to speak for the key. Git prints the status on standard output in
// raw mode and the human line on standard error, and older versions that do not know the
// flag fail the command — which is a refusal, and therefore the safe direction.
func gateReceiptVerifySignature(root, ref, signers string) (string, error) {
	args := gateReceiptGitArgs(root, "-c", "gpg.format=ssh",
		"-c", "gpg.ssh.allowedSignersFile="+signers, "verify-commit", "--raw", ref)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git verify-commit %s: %w", ref, err)
	}
	// Standard error comes first because it is the stream the message cannot reach: the
	// receipt is the commit's message, so a receipt could otherwise carry a line that looks
	// like a signature status and be reported as one.
	if line := gateReceiptSignatureLine(stderr.String()); line != "" {
		return line, nil
	}
	if line := gateReceiptSignatureLine(stdout.String()); line != "" {
		return line, nil
	}
	// A git that verified the signature without describing it in a line this recognises
	// still verified it; the report is what is missing, and the receipt is still evidence.
	for _, output := range []string{stderr.String() + "\n" + stdout.String()} {
		for _, line := range splitLines(output) {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed, nil
			}
		}
	}
	return "", errors.New("git verify-commit printed no signature status")
}

// gateReceiptSignatureLine is the line of git's output that names the key a signature was
// made with, or the empty string when the output carries no such line.
func gateReceiptSignatureLine(output string) string {
	for _, line := range splitLines(output) {
		if strings.Contains(line, " signature for ") {
			return line
		}
	}
	return ""
}

// gateReceiptDocsOnly asks whether a change is documentation only, through the same command
// the gate's own scope decision and CI's scope job ask, so a receipt's scope cannot drift
// from either. It runs in the repository being verified, because that repository's change is
// what it classifies.
func gateReceiptDocsOnly(root, base, sha string) (bool, error) {
	fields := strings.Fields(os.Getenv(gateScopeCmdEnv))
	name, prefix := "go", []string{"run", "./cmd/buildtool", "scope"}
	if len(fields) > 0 {
		name, prefix = fields[0], fields[1:]
	}
	args := append(append([]string{}, prefix...),
		"--base", base, "--head", sha, "--rule", gateScopeRule)
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s %s: %w: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	// Anything but an explicit true would leave a docs-only receipt refused and a full one
	// accepted, so an answer this does not understand fails closed.
	return strings.TrimSpace(stdout.String()) == "true", nil
}

// gateReceiptBody is the message of the receipt commit: everything after the header block.
// The signature rides in the `gpgsig` header, so unlike a signed tag there is no signature
// block at the end of the message to strip.
func gateReceiptBody(commit string) string {
	_, body, found := strings.Cut(commit, "\n\n")
	if !found {
		return ""
	}
	return body
}

// gateReceiptOrNothing renders a parent that could not be resolved, which happens for a
// receipt commit with no parent at all: the refusal still has to name what it found.
func gateReceiptOrNothing(parent string) string {
	if parent == "" {
		return "nothing"
	}
	return parent
}

// stringList collects a repeatable flag's values in the order they were given, which is the
// order a receipt lists its checks in and the order a refusal should report them.
type stringList []string

// String implements flag.Value.
func (l *stringList) String() string { return strings.Join(*l, ",") }

// Set implements flag.Value.
func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

// runGateReceiptShow prints the receipt for a commit: this clone's local file when it has
// one, and the published receipt otherwise. The local file wins because it is the run this
// clone performed and is the one a person is usually inspecting.
func runGateReceiptShow(root string, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("gate-receipt show", flag.ContinueOnError)
	flags.SetOutput(errOut)
	sha := flags.String("sha", "", "the commit whose receipt is printed; omitted, HEAD")
	if err := parse(flags, args); err != nil {
		return usageError{err.Error()}
	}

	target := *sha
	if target == "" {
		target = "HEAD"
	}
	resolved, err := gitPathIn(root, "rev-parse", "--verify", target+"^{commit}")
	if err != nil {
		return usageError{fmt.Sprintf("show: not a commit: %s", target)}
	}

	if payload, err := gateReceiptLocal(root, resolved); err == nil {
		fmt.Fprint(out, payload)
		return nil
	}
	ref := gateReceiptRefPrefix + "/" + resolved
	if _, err := gitPathIn(root, "rev-parse", "--verify", "--quiet", ref); err == nil {
		body, err := gitOutputIn(root, "cat-file", "commit", ref)
		if err != nil {
			return err
		}
		fmt.Fprint(out, gateReceiptBody(body))
		return nil
	}
	return refuse(errOut, "no receipt for %s: this clone has no local file and %s is not published", resolved, ref)
}

// gitRun runs one git command for its effect, discarding its output. A failure carries the
// command's own text, because for a ref update or a push that text is the reason.
func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", gateReceiptGitArgs(dir, args...)...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitInput runs one git command with text on its standard input and returns its standard
// output. It is how the changed path list reaches `git hash-object`, whose object format is
// the repository's and not this command's to choose.
func gitInput(dir, input string, args ...string) (string, error) {
	cmd := exec.Command("git", gateReceiptGitArgs(dir, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gateReceiptGitArgs names the repository a git command runs in, when a caller knows it.
//
// Naming it through `-C` rather than through the process's working directory is deliberate:
// this command is started by the gate in a worktree and by a hook in whatever directory the
// push came from, and `-C` is the one form both can pass. An empty directory means the
// caller wants git's own resolution, which is what the unqualified commands in this package
// already do.
func gateReceiptGitArgs(dir string, args ...string) []string {
	if dir == "" {
		return args
	}
	return append([]string{"-C", dir}, args...)
}
