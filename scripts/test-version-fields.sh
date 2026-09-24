#!/usr/bin/env bash
# Behaviour tests for check-version-fields.sh. Builds a throwaway repository so
# each case is a real git history rather than a mocked one.
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
check="$script_dir/check-version-fields.sh"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

cd "$scratch"
git init -q .
git config user.email "test@example.invalid"
git config user.name "test"

printf -- '---\ntitle: Versioned\nversion: v1\n---\n\n# Versioned\n\nbody\n' >spec.md
printf -- '---\ntitle: Plain\n---\n\n# Plain\n\nbody\n' >plain.md
git add spec.md plain.md
git commit -qm base
base="$(git rev-parse HEAD)"

fail=0
expect() { # <expected-rc> <label> <expected-output> [<path>...]
  local want_rc="$1" label="$2" want_out="$3" rc=0 out=""
  shift 3
  set +e
  out="$("$check" "$base" "$@" 2>&1)"
  rc=$?
  set -e
  if [[ "$rc" != "$want_rc" ]]; then
    echo "FAIL $label: exit $rc, want $want_rc" >&2
    fail=1
    return
  fi
  if [[ "$out" != "$want_out" ]]; then
    echo "FAIL $label: output '$out', want '$want_out'" >&2
    fail=1
    return
  fi
  echo "ok - $label"
}

# 1. A versioned file changed with its version untouched is reported.
printf -- '---\ntitle: Versioned\nversion: v1\n---\n\n# Versioned\n\nmaterial change\n' >spec.md
expect 1 "unbumped versioned change is reported" "spec.md" spec.md

# 2. Bumping the version clears it.
printf -- '---\ntitle: Versioned\nversion: v2\n---\n\n# Versioned\n\nmaterial change\n' >spec.md
expect 0 "bumped versioned change passes" "" spec.md

# 3. A file without the field is not a versioned file.
printf -- '---\ntitle: Plain\n---\n\n# Plain\n\nchanged\n' >plain.md
expect 0 "unversioned change passes" "" plain.md

# 4. Both at once: only the offender is named. spec.md goes back to the base
# version, so its change is unbumped again while plain.md's is not versioned.
printf -- '---\ntitle: Versioned\nversion: v1\n---\n\n# Versioned\n\nmaterial change\n' >spec.md
printf -- '---\ntitle: Plain\n---\n\n# Plain\n\nchanged again\n' >plain.md
expect 1 "mixed change names only the versioned offender" "spec.md" spec.md plain.md

# 5. A versioned file that is new to the history has nothing to compare.
printf -- '---\ntitle: Fresh\nversion: v1\n---\n\n# Fresh\n' >fresh.md
expect 0 "new versioned file passes" "" fresh.md

# 6. A missing path is skipped rather than fatal. spec.md is bumped first, so the
# only interesting input is the path that does not exist.
printf -- '---\ntitle: Versioned\nversion: v2\n---\n\n# Versioned\n\nmaterial change\n' >spec.md
expect 0 "missing path is skipped" "" spec.md gone.md

# 7. No base revision is a usage error.
set +e
"$check" >/dev/null 2>&1
rc=$?
set -e
if [[ "$rc" != 2 ]]; then
  echo "FAIL no base revision: exit $rc, want 2" >&2
  fail=1
else
  echo "ok - no base revision is a usage error"
fi

if [[ "$fail" != 0 ]]; then
  echo "version-field check tests FAILED" >&2
  exit 1
fi
echo "version-field check tests passed"
