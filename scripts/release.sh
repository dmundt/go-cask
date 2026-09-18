#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

usage() {
  cat <<'EOF'
usage: ./scripts/release.sh <tag> [from-tag] [--dry-run] [--publish]

Generate release notes from CHANGELOG.md and optionally publish a GitHub release.
If --publish is set, the script uploads the generated notes via `gh release create`.
EOF
}

tag=""
from_tag=""
dry_run=0
publish=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --dry-run)
      dry_run=1
      ;;
    --publish)
      publish=1
      ;;
    --from-tag)
      shift
      from_tag="${1:-}"
      ;;
    *)
      if [[ -z "$tag" ]]; then
        tag="$1"
      elif [[ -z "$from_tag" ]]; then
        from_tag="$1"
      else
        echo "unexpected argument: $1" >&2
        usage >&2
        exit 1
      fi
      ;;
  esac
  shift
done

if [[ -z "$tag" ]]; then
  usage >&2
  exit 1
fi

if [[ -z "$from_tag" ]]; then
  from_tag="$(git tag --sort=-version:refname | awk -v tag="$tag" '$0 != tag { print; exit }' || true)"
fi

if [[ "$publish" -eq 1 ]]; then
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "working tree must be clean before publishing a release" >&2
    exit 1
  fi
  if ! git rev-parse --verify --quiet "${tag}^{commit}" >/dev/null; then
    echo "release tag $tag does not exist locally" >&2
    exit 1
  fi
  if [[ "$(git rev-list -n 1 "$tag")" != "$(git rev-parse HEAD)" ]]; then
    echo "release tag $tag must point at HEAD" >&2
    exit 1
  fi
  if ! git rev-parse --verify --quiet 'main^{commit}' >/dev/null; then
    echo "main branch does not exist locally" >&2
    exit 1
  fi
  if ! git merge-base --is-ancestor "$tag" main; then
    echo "release tag $tag must be reachable from main" >&2
    exit 1
  fi
fi

notes="$(bash ./scripts/release-notes.sh "$tag" "$from_tag")"

if [[ "$dry_run" -eq 1 ]]; then
  printf '%s\n' "$notes"
  exit 0
fi

if [[ "$publish" -eq 1 ]]; then
  gh_bin="$(command -v gh || command -v gh.exe || true)"
  if [[ -z "$gh_bin" ]]; then
    echo "gh is required for --publish" >&2
    exit 1
  fi
  printf '%s\n' "$notes" |
    "$gh_bin" release create "$tag" --title "$tag" --notes-file - --verify-tag
  exit 0
fi

printf '%s\n' "$notes"
