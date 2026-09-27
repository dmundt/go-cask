# buildtool

The entry point for the repository's build decisions: developer tooling, not the
product CLI. It reads the repository, calls the engine in
[`internal/build/core`](../../internal/build/core/README.md), applies go-cask's
tables from [`internal/build/policy`](../../internal/build/policy/README.md), and
prints a verdict with an exit status a gate step can act on.

## Commands

| Command | Decides |
|---|---|
| `verify` | the gate: every step in order, with the scope, the concurrency and the escape hatches decided by `internal/build/core/verify` and go-cask's tables from `internal/build/policy`. It writes the gate stamp for a complete run and, for a clean tree, hands `scripts/gate-receipt.sh` the receipt CI reuses — the check names come from `policy.Verify().Checks`, and the policy tests pin them against that helper's `suite_full`. `scripts/verify.sh` is this command's name for the gate |
| `layer-matrix` | every package's imports against the layer table |
| `coverage-tier` | that every `cas/` package carries a tier or a written exemption; `--list` prints the gate's measurement table |
| `coverage-check` | the thresholds, reading one `threshold\|package\|measured` line per package from stdin — the gate collects the measurements, this decides |
| `markdown-integrity` | every tracked `.md`: raw HTML, forbidden fences, dead links, the CHANGELOG structure, mermaid balance |
| `website-examples` | that each Go fence on the site is a complete unit, that the inventory tables match the tree, and that the materialized set builds and vets |
| `website-footer` | that the published footer is the pinned one-line contract, that the machinery the redesign deleted stays deleted, and that the site hook's self-test still renders the pinned line |
| `scope` | which of CI's jobs a change set can affect, and whether the gate may run the documentation scope; `--rule` prints one verdict as a bare `true`/`false` |
| `security` | that the vulnerability scan runs with the pinned scanner, installing it when the binary on `PATH` is not that release |
| `bench-baseline` | the committed benchmark reference dump: its capture, its archive-before-refresh order, and `--capture-only` leaving it untouched |
| `bench-compare` | that a comparison chooses its baseline before capturing and never writes the reference; a missing `benchstat` keeps the documented exit status 2 |
| `run-examples` | which example programs a runner executes, with which arguments and store, and which one it must never run |
| `land-lane` | the local advisory slot: its `status`/`whoami`/`acquire`/`renew`/`release` verbs, its idle-time staleness rule and the takeover record an eviction leaves |
| `pr-lane` | the server-side lane: its `claim`/`check`/`status`/`release`/`whoami` verbs, the ref that is the compare-and-swap, the open pull request that is the lease, and the claim window past which a claim with no pull request is taken over; the verdict is `internal/build/core/claim`'s |
| `pre-push` | the mechanical landing rule a push must satisfy, and the advisory-slot note; the rules are `internal/build/core/gate`'s |
| `worktree` | that a task worktree's `.git` link is relative and resolves to its own admin directory, that its registration is locked, and that `prune` refuses |
| `codec-guards` | that `gitlike` and `cas/pack` do not reach the codec layer transitively |
| `module-graph` | that `go list -m` names this module as the main one |
| `dep-graph` | that the committed package graph is current; `--write` is the only mode that touches the file |
| `version-fields` | that a changed versioned file moved its frontmatter `version:`; `--base <rev>` is required |
| `release-notes` | a tag's GitHub release note: its changelog section reshaped, closed with the compare link |
| `release` | the same note, and with `--publish` the GitHub release, after the tag/tree/main guards pass |

```bash
go run ./cmd/buildtool layer-matrix
go run ./cmd/buildtool dep-graph --write
go run ./cmd/buildtool release --tag v1.3.0 --dry-run
```

## Exit status

- `0` — the rule holds.
- `1` — the rule failed, or a command could not run. The offending packages, paths or
  file:line positions are on stderr.
- `2` — the invocation was wrong (a missing `--base` or `--tag`, an unknown command,
  a stray argument), or a step cannot run at all for a reason the command documents —
  `bench-compare` exits 2 when `benchstat` is not installed, which the helper it
  replaced also did. The distinction matters: `2` is never a verdict on the tree.

A command that prints a list prints only the list on stdout, so a gate can capture it
in a command substitution — `version-fields`, `coverage-tier --list` and
`scope --rule` are the three that do.

## Shape

Each command is thin: it obtains what only the tool can (a `go list`, the tracked file
list, a `git` call, the repository root), calls the engine, and reports. No rule is
implemented here. When a command needs to decide something, that decision belongs in
`internal/build/core` with a test, and go-cask's answer to it belongs in
`internal/build/policy`.

`run(args, out, errOut)` is separate from `main` so the command surface can be driven
from a test without a process.

`verify` is the one command that orchestrates rather than decides: it runs the steps in
order, streams their output, and writes the gate stamp. The decisions stay out of it —
which steps a scope runs, whether an escape hatch dropped one, and how many packages may be
built at once are `internal/build/core/verify`'s — so the step list is the only thing here
that can go stale, and a step is one entry in `gateSteps`.

## Testing

`go test ./cmd/buildtool/` — the target-list format the gate measures from, the
invocation errors, help, and the `go list` output parsing.
