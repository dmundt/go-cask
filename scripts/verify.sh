#!/usr/bin/env bash
# The repository's verification gate.
#
# The step list is not here: it is `go run ./cmd/buildtool verify`, where it has tests and
# where the decisions behind it — what a run covers, how many packages it builds at once,
# and whether an escape hatch dropped a step — live in `internal/build/core/verify` with
# go-cask's answers in `internal/build/policy`. What is left is the reason this file
# exists at all: `./scripts/verify.sh` is the name CI, `.githooks/pre-push` and the
# specification set call the gate by, and a name that moved would have to be chased through
# every one of them.
#
# The toolchain comes from `buildtool.sh`, which sources the shared resolution first: Git
# Bash and WSL may not inherit Go's path, and PATH can only be changed in the caller's
# shell.
#
# Run it from the repository root. A green run ends with `verification passed`; a run that
# stops earlier failed, and a run that skipped a step says so and records nothing.
set -euo pipefail

exec "$(cd "$(dirname "$0")" && pwd -P)/buildtool.sh" verify "$@"
