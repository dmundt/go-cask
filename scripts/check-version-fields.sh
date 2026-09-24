#!/usr/bin/env bash
# Report versioned files that changed without their frontmatter `version:` moving.
#
# A file is "versioned" when its frontmatter block (a leading `---` … `---`) has a
# `version:` field; docs/AGENT.md requires that field to move on a material change.
# Nothing checked it, and the rule had already failed: #238 rewrote AGENTS.md and
# docs/index.md without bumping either, and only a human-filed follow-up caught it.
#
# This judges the presence of a bump, never materiality — the check cannot tell a
# typo from a contract change, and a false positive costs one line while the miss
# cost three pull requests.
#
#   scripts/check-version-fields.sh <base-rev> [<path>...]
#
# Prints one offending path per line and exits 1 when any is found, 0 otherwise.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <base-rev> [<path>...]" >&2
  exit 2
fi

base="$1"
shift

# The version value of the frontmatter block on stdin, or nothing.
extract_version() {
  awk '
    NR == 1 && $0 != "---" { exit }
    NR == 1 { next }
    /^---[[:space:]]*$/ { exit }
    /^version:[[:space:]]*/ { sub(/^version:[[:space:]]*/, ""); print; exit }
  '
}

status=0
for path in "$@"; do
  [[ -n "$path" && -f "$path" ]] || continue
  now="$(extract_version <"$path")"
  [[ -n "$now" ]] || continue
  # A path that does not exist in the base revision has nothing to compare; the
  # `|| true` keeps `set -e -o pipefail` from treating that as a failure.
  was="$(git show "${base}:${path}" 2>/dev/null | extract_version || true)"
  [[ -n "$was" ]] || continue
  if [[ "$now" == "$was" ]]; then
    echo "$path"
    status=1
  fi
done

exit "$status"
