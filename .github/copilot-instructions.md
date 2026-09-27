# go-cask — Copilot instructions

[AGENTS.md](../AGENTS.md) is the repo-root agent aggregator and the authority on
every convention here. This file exists only because some Copilot surfaces read
this path and not that one: read [AGENTS.md](../AGENTS.md) first, then match the
path you are editing against the rule table in [docs/index.md](../docs/index.md)
(longest match wins) and read the file it names.

The [cask-change skill](../.agents/skills/cask-change/SKILL.md) is the change
playbook for `cas/`, a backend, a codec, `cmd/cask`, `internal/web`, `gitlike/`,
`examples/` and `scripts/`.

No rule lives here, deliberately: a duplicated rule drifts, and the copy is what
an agent follows.
