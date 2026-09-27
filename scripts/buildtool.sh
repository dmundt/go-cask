#!/bin/sh
# The build tool's one entry point: resolve the toolchain, then run cmd/buildtool.
#
# Every purpose the build process has is a subcommand of the tool — the gate (verify), the
# task worktrees, the landing lane, the security scan, the examples, the benchmarks, the
# release notes — so this shim stays small: it finds Go from whichever toolchain the caller
# is in, runs the tool from the repository it belongs to, and passes the arguments through
# unchanged. The rules are all in Go; the shell only starts it.
#
# `./scripts/verify.sh` is the gate's own name for `buildtool.sh verify`, because that
# decision is the one every document and the pre-push hook call by name.
#
# It runs `go run`, so the first call in a fresh worktree compiles the tool; the build cache
# makes every call after that cheap.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
# shellcheck source=scripts/toolchain.sh
. "$here/toolchain.sh"

cd "$here/.."
exec go run ./cmd/buildtool "$@"
