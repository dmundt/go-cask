#!/usr/bin/env bash
# ARCHIVED — superseded by `go run ./cmd/buildtool run-examples`.
#
# Which examples exist, which of them terminate on their own and which arguments
# complete each are now a table (internal/build/policy) read by a tested selection rule
# (internal/build/examples); the command runs them. Nothing runs this copy; see
# ../shell/README.md.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

# The automated examples each terminate on their own. examples/api does not: it
# is a manual two-process pair — examples/api/server blocks until interrupted and
# examples/api/demo needs that server running (examples/api/README.md) — so the
# script names it in --list and prints how to start it instead of running it.
automated=(artifacts bloom files notes pack)

# Scratch root for the stores the examples create. It lives inside the module,
# because the examples resolve their package path from the working directory,
# and it is removed on exit, so a run leaves the working tree untouched.
mkdir -p "$repo_root/.gocache"
scratch="$(mktemp -d "$repo_root/.gocache/run-examples.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT

api_commands="go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080
go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md"

if [[ "${1:-}" == "--list" ]]; then
  echo "automated examples:"
  for name in "${automated[@]}"; do
    printf '  %s\n' "$name"
  done
  echo "manual examples:"
  echo "  api (two-process pair: examples/api/server, then examples/api/demo)"
  exit 0
fi

if [[ $# -gt 0 ]]; then
  selected=("$@")
else
  selected=("${automated[@]}")
fi

for name in "${selected[@]}"; do
  if [[ "$name" == "api" ]]; then
    echo "the api example is a manual two-process pair; start it in two terminals:" >&2
    printf '%s\n' "$api_commands" >&2
    exit 1
  fi
  if [[ ! -d "./examples/$name" ]]; then
    echo "unknown example: $name" >&2
    exit 1
  fi

  # One terminating command per example: artifacts and files are CLIs, so they
  # need their subcommand, and every store they create lands in the scratch root.
  args=()
  case "$name" in
    artifacts) args=(-store "$scratch/artifacts" stats) ;;
    files) args=(-store "$scratch/files" stats) ;;
    pack) args=(roundtrip 8 "hello world") ;;
  esac

  echo "== $name =="
  (cd "$scratch" && go run "$repo_root/examples/$name" ${args[@]+"${args[@]}"})
done

if [[ $# -eq 0 ]]; then
  echo
  echo "note: the api example is a manual two-process pair; start it in two terminals:"
  printf '%s\n' "$api_commands"
fi
