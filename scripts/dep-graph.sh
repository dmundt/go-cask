#!/usr/bin/env bash
set -euo pipefail

# dep-graph.sh — draw the local package dependency graph as Mermaid, from `go list`.
#
# Usage:
#   ./scripts/dep-graph.sh            regenerate docs/design/package-graph.md
#   ./scripts/dep-graph.sh --check    fail when that file is stale; write nothing
#
# Ownership (scripts/AGENT.md: "A script that owns a committed artifact must own
# it exclusively"): this script owns docs/design/package-graph.md. `--check` is the
# mode that never touches it — it regenerates into a scratch directory and
# compares. scripts/test-dep-graph.sh pins that split, and verify.sh runs
# `--check`, so a change that adds, removes or re-points a local import must
# regenerate the document in the same change.
#
# Only production imports are drawn: `go list`'s .Imports omits imports that
# appear solely in _test.go files, so the diagram is the consumer-visible build
# graph. Standard-library packages are not drawn at all.
#
# The local package and edge sets are invariant across GOOS/GOARCH (checked
# against windows/amd64, linux/amd64 and darwin/arm64: identical output), and
# only the stdlib side of a package is build-context sensitive, so the committed
# file is reproducible on any host. Everything is sorted under LC_ALL=C.
#
# The frontmatter `version` moves by one when the generated body changes and is
# left alone when it does not, so scripts/check-version-fields.sh sees a bump
# exactly when this artifact materially changed, and regenerating an unchanged
# graph is a byte-for-byte no-op that touches no file.
#
# Exit codes: 0 success, 1 the graph is stale or could not be derived, 2 usage.

doc_rel="docs/design/package-graph.md"
doc_title="Package Dependency Graph"
doc_description="Generated dependency graph of every package in the go-cask module, derived from go list and owned by scripts/dep-graph.sh."

usage() {
  cat <<'USAGE'
Usage: ./scripts/dep-graph.sh [--check]

  (no option)  Regenerate docs/design/package-graph.md from `go list`.
  --check      Regenerate into a scratch directory and exit 1 when the
               committed document differs. Never writes the document.

The graph is derived from the module's own packages; edit this script, not the
generated document, when the diagram's shape needs to change.
USAGE
}

mode=write
case "${1:-}" in
-h | --help)
  usage
  exit 0
  ;;
--check)
  mode=check
  ;;
"") ;;
*)
  echo "dep-graph.sh: unknown option '$1'" >&2
  usage >&2
  exit 2
  ;;
esac
if [[ $# -gt 1 ]]; then
  echo "dep-graph.sh: at most one option is accepted" >&2
  usage >&2
  exit 2
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"
doc="$repo_root/$doc_rel"

# The module path the graph is drawn relative to: every package shorter label is
# this path with its trailing slash removed.
if ! module="$(go list -m -f '{{.Path}}' 2>/dev/null)"; then
  echo "dep-graph.sh: 'go list -m' failed — run this from inside the Go module" >&2
  exit 1
fi
if [[ -z "$module" ]]; then
  echo "dep-graph.sh: 'go list -m' reported no module path" >&2
  exit 1
fi

sort_unique() { printf '%s' "$1" | LC_ALL=C sort -u; }

# Mermaid node ids may not contain '/' or '.', and the short package path is the
# label the reader sees.
node_id() { printf '%s' "${1//[^A-Za-z0-9]/_}"; }

layer_of() {
  case "$1" in
  cas) printf 'CORE' ;;
  cas/backend | cas/backend/*) printf 'BYTE' ;;
  cas/*) printf 'HELP' ;;
  internal/*) printf 'INTERNAL' ;;
  *) printf 'APPS' ;;
  esac
}

layer_title() {
  case "$1" in
  APPS) printf 'Applications - gitlike, cmd/cask, examples, benchmarks' ;;
  INTERNAL) printf 'internal - not importable outside the module' ;;
  HELP) printf 'cas helper and typed layer' ;;
  BYTE) printf 'cas/backend - byte layer' ;;
  CORE) printf 'cas - generic core' ;;
  esac
}

# generate_body writes the diagram alone: H1, intro and the trailing sections are
# static prose owned by document().
generate_body() {
  local rows path imports imp target had
  local raw_nodes="" raw_edges="" raw_leaves=""

  rows="$(go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...)"
  while IFS='|' read -r path imports; do
    [[ -n "$path" ]] || continue
    [[ "$path" == "$module/"* ]] || continue
    path="${path#"$module"/}"
    raw_nodes="${raw_nodes}${path}"$'\n'
    had=0
    for imp in $imports; do
      [[ "$imp" == "$module/"* ]] || continue
      target="${imp#"$module"/}"
      raw_nodes="${raw_nodes}${target}"$'\n'
      raw_edges="${raw_edges}${path}>${target}"$'\n'
      had=1
    done
    # A package with no local import is a leaf of the graph; the reader sees that
    # as the filled style rather than having to trace every arrow.
    [[ "$had" -eq 1 ]] || raw_leaves="${raw_leaves}${path}"$'\n'
  done <<<"$rows"

  local nodes edges leaves group node edge leaf ids=""
  nodes="$(sort_unique "$raw_nodes")"
  edges="$(sort_unique "$raw_edges")"
  leaves="$(sort_unique "$raw_leaves")"

  printf 'flowchart TD\n'
  for group in APPS INTERNAL HELP BYTE CORE; do
    printf '  subgraph %s["%s"]\n' "$group" "$(layer_title "$group")"
    while read -r node; do
      [[ -n "$node" ]] || continue
      [[ "$(layer_of "$node")" == "$group" ]] || continue
      printf '    %s["%s"]\n' "$(node_id "$node")" "$node"
    done <<<"$nodes"
    printf '  end\n'
  done
  printf '\n'
  while read -r edge; do
    [[ -n "$edge" ]] || continue
    printf '  %s --> %s\n' "$(node_id "${edge%%>*}")" "$(node_id "${edge#*>}")"
  done <<<"$edges"
  printf '\n'
  printf '  classDef leaf fill:#eef7ee,stroke:#4a7c59,color:#12321c\n'
  while read -r leaf; do
    [[ -n "$leaf" ]] || continue
    ids="${ids:+$ids,}$(node_id "$leaf")"
  done <<<"$leaves"
  [[ -z "$ids" ]] || printf '  class %s leaf\n' "$ids"
}

frontmatter() {
  printf -- '---\n'
  printf 'type: Design Document\n'
  printf 'title: %s — go-cask\n' "$doc_title"
  printf 'description: %s\n' "$doc_description"
  printf 'version: %s\n' "$1"
  printf 'generated: scripts/dep-graph.sh\n'
  printf -- '---\n'
}

# document writes the whole file. The heredocs are quoted, so their backticks and
# dollar signs are literal Markdown.
document() {
  frontmatter "$1"
  cat <<'PROSE'

# Package Dependency Graph — go-cask

The local dependency graph of every package in this module, grouped by layer. An
edge points from the importing package to the package it imports, so the
applications sit at the top and `cas` — which imports no local package at all —
sits at the bottom.

The diagram is generated from `go list` by `scripts/dep-graph.sh`; edit that
script, not this file. Only production imports are drawn: imports that appear
solely in `_test.go` files are excluded and the standard library is not drawn, so
this is the consumer-visible build graph. The local package and edge sets do not
vary with `GOOS` or `GOARCH`, so the file is reproducible on any host.
PROSE
  printf '\n```mermaid\n'
  generate_body
  printf '```\n'
  cat <<'PROSE'

## Layers

| Layer | Subgraph | Holds |
|---|---|---|
| Core | `CORE` | The generic `cas` core: `Store[T]`, `Digest`, `Hasher`, `Codec[T]`, the envelope. |
| Byte layer | `BYTE` | `cas/backend` and its `fs`, `mem`, `packfs` and `snapshot` implementations. |
| Helpers | `HELP` | The typed and maintenance layer under `cas/`: codecs, hashers, caches, bloom filters, verification, `pack`, `refs`, `repo`. |
| Internal | `INTERNAL` | `internal/*`, not importable outside this module. |
| Applications | `APPS` | `gitlike`, `cmd/cask`, `examples/*` and `benchmarks`. |

Packages that import no local package are drawn as leaves.

## Regenerating

```bash
./scripts/dep-graph.sh            # rewrite this file from go list
./scripts/dep-graph.sh --check    # report staleness; writes nothing
```

`scripts/verify.sh` runs `--check`, so a change that adds, removes or re-points a
local import must regenerate this file in the same change. Regeneration is
idempotent: when the graph is unchanged the script rewrites nothing and leaves
the frontmatter `version` alone; when the graph changes, it bumps that version by
one.
PROSE
}

# version_of reads the frontmatter `version` of a document, or nothing.
version_of() {
  awk '
    NR == 1 && $0 != "---" { exit }
    NR == 1 { next }
    /^---[[:space:]]*$/ { exit }
    /^version:[[:space:]]*/ { sub(/^version:[[:space:]]*/, ""); print; exit }
  ' "$1"
}

bump() {
  local n="${1#v}"
  if [[ ! "$n" =~ ^[0-9]+$ ]]; then
    echo "dep-graph.sh: cannot read the frontmatter version '$1' in $doc_rel" >&2
    exit 1
  fi
  printf 'v%d\n' "$((n + 1))"
}

# Scratch lives under the ignored .gocache/ so the final `mv` stays on one
# filesystem (a linked worktree starts without that directory).
mkdir -p "$repo_root/.gocache"
scratch="$(mktemp -d "${repo_root}/.gocache/dep-graph.XXXXXX")"
trap 'rm -rf "${scratch:-}"' EXIT

if [[ -f "$doc" ]]; then
  committed_version="$(version_of "$doc")"
else
  committed_version=""
fi
# A document without a readable version starts at v1, like every generated file.
[[ -n "$committed_version" ]] || committed_version="v1"

document "$committed_version" >"$scratch/current.md"

if [[ -f "$doc" ]] && cmp -s "$scratch/current.md" "$doc"; then
  if [[ "$mode" == "check" ]]; then
    echo "dep-graph.sh: $doc_rel is up to date"
  else
    echo "dep-graph.sh: $doc_rel is up to date — nothing written"
  fi
  exit 0
fi

if [[ "$mode" == "check" ]]; then
  echo "dep-graph.sh: $doc_rel is stale" >&2
  echo "  the module's local package graph changed since that file was generated" >&2
  echo "  regenerate it with: ./scripts/dep-graph.sh" >&2
  exit 1
fi

# The body changed (or the file is new): move the version with the artifact — a
# brand-new document starts at v1, an existing one moves by one — then replace
# the file atomically.
if [[ -f "$doc" ]]; then
  next_version="$(bump "$committed_version")"
else
  next_version="$committed_version"
fi
document "$next_version" >"$scratch/next.md"
mv "$scratch/next.md" "$doc"
echo "dep-graph.sh: wrote $doc_rel"
