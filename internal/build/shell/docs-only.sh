#!/usr/bin/env bash
# ARCHIVED — superseded by `go run ./cmd/buildtool scope`.
#
# This script classified a Git diff as documentation-only. The rule now lives in
# internal/build/changes (the classification) and internal/build/policy (the
# patterns), and the gate and CI's scope job both call the command above, so one
# table serves them instead of a pattern list held in sync by a comment. Nothing
# runs this copy; see ../shell/README.md.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 BASE_SHA HEAD_SHA" >&2
  exit 2
fi

docs_only=true
changed=false
while IFS= read -r path; do
  changed=true
  case "$path" in
    *.md|docs/*|website/*|mkdocs.yml|requirements-docs.txt|requirements-docs.lock|.github/workflows/website.yml) ;;
    *) docs_only=false; break ;;
  esac
done < <(git diff --name-only "$1" "$2")

if [[ "$changed" == true && "$docs_only" == true ]]; then
  echo true
else
  echo false
fi
