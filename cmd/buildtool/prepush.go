package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dmundt/go-cask/internal/build/core/gate"
	"github.com/dmundt/go-cask/internal/build/core/lane"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// prePushAdvisory is the note a push gets when this worktree does not hold the local
// advisory slot. It is a note and never a refusal: the lane is the pull request on the
// server, which every clone and the operator can see, and a per-clone file must not be
// able to stop a landing the server would have serialized anyway.
const prePushAdvisory = "pre-push: note — this worktree does not hold the local advisory slot.\n" +
	"  Not required: the lane is the pull request, and 'buildtool pr-lane status'\n" +
	"  shows it. The local slot only keeps two gate runs in one clone from\n" +
	"  overlapping, so a busy clone may still want it (buildtool land-lane acquire).\n"

// prePushNoReceipt is the note a push gets when the gate receipt could not be published.
// Like the advisory it is a note and never a refusal: CI falls back to running the whole
// gate, which is the safe direction, and a toolchain whose git cannot sign cannot publish
// at all — the WSL shell next to a Windows signing key is that case on this host.
const prePushNoReceipt = "pre-push: note — the gate receipt was not published for this commit, so CI will run\n" +
	"  the whole gate again. Publish it from the toolchain that signs your commits:\n" +
	"      ./scripts/gate-receipt.sh publish\n" +
	"  (it needs gpg.format and user.signingkey; see scripts/AGENT.md)\n"

// runPrePush applies the mechanical landing rules a push must satisfy.
//
// One rule is hard: the gate ran green for the exact commit being pushed, which is what
// the shared ledger records. The ledger is keyed by commit, so re-pushing an unchanged
// commit costs nothing, and it is a ledger rather than a slot, so a gate run in another
// worktree of this clone cannot invalidate this branch's stamp.
//
// The hook receives the remote's name and URL as arguments; only the name is used, and only
// to publish the gate receipt to the right remote.
func runPrePush(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("pre-push", flag.ContinueOnError)
	flags.SetOutput(errOut)
	repo := flags.String("repo", "", "the pushed repository; omitted, the working directory")
	remote := flags.String("remote", "origin", "the remote the gate receipt is published to")
	if err := parse(flags, args); err != nil {
		return err
	}

	table := policy.Gate()
	commonDir, err := gitPathIn(*repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	head, err := gitPathIn(*repo, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	ledger := readFileOrEmpty(filepath.Join(commonDir, table.Ledger))
	if !gate.Verified(ledger, head) {
		fmt.Fprintf(errOut, "pre-push: no green gate for %s.\n"+
			"  Run the gate once for this commit, then push again:\n"+
			"      %s                      # docs-only branches get the docs gate\n"+
			"      VERIFY_SCOPE=full %s    # force the whole gate\n", head, table.Verify, table.Verify)
		// The message is already on stderr, which is where a hook's refusal belongs.
		return exitStatus(1)
	}

	// The local slot is advisory. Asking about it must never fail the push, so a slot
	// that cannot be resolved at all is a reason to say nothing.
	if slot, err := resolveLandLane(*repo); err == nil {
		if lane.SlotStatus(slot.read(), slot.who, slot.mine()) != lane.Mine {
			fmt.Fprint(errOut, prePushAdvisory)
		}
	}

	// Last, and best effort: publish the gate receipt for this commit, so CI reuses this
	// run instead of repeating the same suite. It comes after the stamp check because the
	// stamp is what authorises the push; nothing here may refuse one.
	publishReceipt(*repo, *remote, head, errOut)
	return nil
}

// publishReceipt signs the receipt as a commit object and pushes it to the coordination ref
// CI reads (scripts/gate-receipt.sh). Every failure is reported and swallowed: the receipt
// only saves CI the work, and a clone that cannot sign simply leaves CI to run the gate.
//
// The helper is still shell, which is why this is a call rather than a function: porting it
// retires the call, and `scripts/README.md` records it as the one rule with no Go home yet.
func publishReceipt(repo, remote, head string, errOut io.Writer) {
	if os.Getenv("CASK_GATE_NO_PUBLISH") != "" {
		return
	}
	script := filepath.Join(repo, filepath.FromSlash(policy.Verify().ReceiptScript))
	if _, err := os.Stat(script); err != nil {
		return
	}

	cmd := exec.Command("bash", script, "publish", "--quiet", "--sha", head, "--remote", remote)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if note := strings.TrimSpace(string(output)); note != "" {
		fmt.Fprintf(errOut, "%s\n", note)
	}
	fmt.Fprint(errOut, prePushNoReceipt)
}

// gitPathIn runs one Git query in a repository and returns its single answer.
func gitPathIn(repo string, args ...string) (string, error) {
	output, err := gitOutputIn(repo, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(output)
	if value == "" {
		return "", fmt.Errorf("git %s printed nothing", strings.Join(args, " "))
	}
	return value, nil
}
