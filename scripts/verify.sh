#!/usr/bin/env bash
set -euo pipefail

# Git Bash and WSL may not inherit Go's installation path.
# Resolve the toolchain before running gofmt or Go commands.
if ! command -v go >/dev/null 2>&1 || ! command -v gofmt >/dev/null 2>&1; then
  if command -v powershell.exe >/dev/null 2>&1; then
    if ! command -v go >/dev/null 2>&1; then
      win_go="$(powershell.exe -NoProfile -Command "(Get-Command go -ErrorAction Stop).Source" 2>/dev/null | tr -d '\r' | head -n 1 || true)"
      if [[ -n "$win_go" ]]; then
        export PATH="$(dirname "$win_go"):$PATH"
      fi
    fi
    if ! command -v gofmt >/dev/null 2>&1; then
      win_gofmt="$(powershell.exe -NoProfile -Command "(Get-Command gofmt -ErrorAction Stop).Source" 2>/dev/null | tr -d '\r' | head -n 1 || true)"
      if [[ -n "$win_gofmt" ]]; then
        export PATH="$(dirname "$win_gofmt"):$PATH"
      fi
    fi
  fi
fi

for candidate in \
  "/usr/local/go/bin" \
  "/usr/lib/go/bin" \
  "/mnt/c/Program Files/Go/bin" \
  "/mnt/c/Program Files (x86)/Go/bin" \
  "/c/Program Files/Go/bin" \
  "/c/Program Files (x86)/Go/bin" \
  "/home/$(id -un 2>/dev/null || printf '%s' root)/bin" \
  "/mnt/c/Users/$(id -un 2>/dev/null || printf '%s' root)/go/bin"; do
  if [[ -d "$candidate" ]]; then
    export PATH="$candidate:$PATH"
  fi
done

if command -v go >/dev/null 2>&1 && ! command -v gofmt >/dev/null 2>&1; then
  go_bin="$(dirname "$(command -v go)")"
  if [[ -x "$go_bin/gofmt" || -x "$go_bin/gofmt.exe" ]]; then
    export PATH="$go_bin:$PATH"
  fi
fi

export CGO_ENABLED="${CGO_ENABLED:-1}"

if [[ "${CGO_ENABLED:-1}" != "0" ]] && ! command -v gcc >/dev/null 2>&1 && ! command -v clang >/dev/null 2>&1 && ! command -v cc >/dev/null 2>&1; then
  echo "CGO is required for the race/coverage gate; install a supported C compiler (gcc or clang) and retry." >&2
  echo "On Windows, use a Go release with a supported MinGW-w64 or LLVM toolchain; MSVC may reject Go's race-build flags." >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

# Refuse to gate a tree other than the one this script lives in. A linked
# worktree whose .git records an absolute path from the other toolchain makes git
# walk up to the primary checkout, so the gate would silently test that tree.
script_root="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "$script_root" != "$repo_root" ]]; then
  cat >&2 <<EOF
verify.sh: git resolved '$repo_root', but this script lives in '$script_root'.
  The worktree's .git file holds an absolute path from the other toolchain, so
  git walks up to the primary checkout and the gate would test the WRONG tree.
  Fix it with the relative form, then re-run:
      scripts/worktree.sh remove <name> && scripts/worktree.sh add <name> <branch>
  (scripts/AGENT.md documents the same fix for a worktree created from WSL.)
EOF
  exit 2
fi

if ! find . -name '*.go' -print -quit | grep -q .; then
  echo "No Go sources; verification skipped."
  exit 0
fi

# ---- worktree locks -------------------------------------------------------
# Every linked worktree must carry git's `locked` file. `git worktree prune`
# deletes any registration whose admin `gitdir` cannot be resolved, and that link
# holds one absolute path form, so every worktree the *other* toolchain created
# looks prunable to this one — deleting the registration and with it the index.
# Prune skips locked worktrees, and locking is additive and idempotent, so the
# gate locks rather than fails: nothing to decide, nothing to remember.
common_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
newly_locked=""
if [[ -n "$common_dir" && -d "$common_dir/worktrees" ]]; then
  for adm in "$common_dir"/worktrees/*/; do
    [[ -d "$adm" ]] || continue
    [[ -e "$adm/locked" ]] && continue
    printf 'locked by scripts/verify.sh — never run git worktree prune\n' >"$adm/locked"
    newly_locked="$newly_locked $(basename "$adm")"
  done
fi
if [[ -n "$newly_locked" ]]; then
  echo "== worktree locks =="
  echo "locked unprotected:$newly_locked — a stray 'git worktree prune' would have deleted them"
fi

# ---- scope ----------------------------------------------------------------
# A documentation-only change runs the documentation gate only — the same scope
# CI applies (ci.yml runs just `git diff --check` for a docs-only PR), plus the
# documentation-integrity checks CI does not run for it. This keeps a docs
# rebuild+push at seconds instead of minutes, which is what makes a serialized
# landing lane cheap. The patterns mirror scripts/docs-only.sh: keep them in
# sync. VERIFY_SCOPE=full forces the whole gate; VERIFY_SCOPE=docs asserts the
# documentation scope and fails loudly if the tree is not docs-only.
# The commits and uncommitted paths this run is asked to cover: the branch's own
# commits measured from the merge base with origin/main, plus anything staged or
# unstaged. The scope decision and the version-field check below share this set.
gate_base="$(git merge-base origin/main HEAD 2>/dev/null || true)"
gate_changed=""
if [[ -n "$gate_base" ]]; then
  gate_changed="$(
    {
      git diff --name-only "$gate_base" HEAD
      git diff --name-only
      git diff --name-only --cached
    } | sort -u
  )"
fi

scope="${VERIFY_SCOPE:-auto}"
if [[ "$scope" == "auto" || "$scope" == "docs" ]]; then
  detected=full
  if [[ -n "$gate_base" ]]; then
    changed="$gate_changed"
    if [[ -n "$changed" ]]; then
      detected=docs
      while IFS= read -r changed_path; do
        case "$changed_path" in
        *.md | docs/* | website/* | mkdocs.yml | requirements-docs.txt | requirements-docs.lock | .github/workflows/website.yml) ;;
        *)
          detected=full
          break
          ;;
        esac
      done <<<"$changed"
    else
      detected=full
    fi
  fi
  if [[ "$scope" == "docs" ]]; then
    if [[ "$detected" != "docs" ]]; then
      echo "VERIFY_SCOPE=docs but the change is not documentation-only" >&2
      exit 2
    fi
  else
    scope="$detected"
  fi
fi
if [[ "$scope" != "docs" && "$scope" != "full" ]]; then
  echo "VERIFY_SCOPE must be auto, docs or full (got '$scope')" >&2
  exit 2
fi
echo "== scope: $scope =="
if [[ "$scope" == "docs" ]]; then
  echo "documentation-only change: running the documentation gate (VERIFY_SCOPE=full runs the whole gate)"
fi

# ---- version fields -------------------------------------------------------
# A versioned file that changed must have its frontmatter `version:` moved with
# it (docs/AGENT.md). This runs in both scopes, because a documentation-only
# change is exactly where the bump is owed, and it judges only that a bump
# happened — never whether the change deserved one.
if [[ -n "$gate_base" && -n "$gate_changed" ]]; then
  echo "== version fields =="
  # shellcheck disable=SC2086 # paths are repository-relative and never contain spaces
  unbumped="$(./scripts/check-version-fields.sh "$gate_base" $gate_changed || true)"
  if [[ -n "$unbumped" ]]; then
    cat >&2 <<EOF
verify.sh: versioned file changed without a version bump:
$(echo "$unbumped" | sed 's/^/  /')
  Bump the frontmatter \`version:\` of each file above (docs/AGENT.md, "version
  starts at v1; increment by one on material change"). The gate checks that a
  bump happened, not that the change was material.
EOF
    exit 2
  fi
fi

if [[ "$scope" == "full" ]]; then

# Scratch files for the coverage tier check below. One EXIT trap owns every
# scratch file this script creates: bash keeps a single EXIT trap per shell, so a
# second `trap` would replace this one and leak whatever it does not name, and
# the ${var:-} defaults keep the handler valid when the run ends before a later
# file is assigned. The captured status is restored after the removals so the
# cleanup cannot decide the script's exit status: under `set -e` an unguarded
# failure inside the trap exits 1, and a bare `exit 0` there would turn a failed
# run into a passing one.
cas_packages="$(mktemp)"
tiered_packages="$(mktemp)"
trap 'exit_status=$?; rm -f "${cas_packages:-}" "${tiered_packages:-}" "${tidy_errors:-}" "${mod_snapshot:-}" || true; exit "$exit_status"' EXIT

echo "== gofmt =="
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "$unformatted"
  echo "gofmt needed; run gofmt -w ." >&2
  exit 1
fi

echo "== go mod tidy =="
tidy_errors="$(mktemp)"
if ! tidy_diff="$(go mod tidy -diff 2>"$tidy_errors")"; then
  cat "$tidy_errors" >&2
  rm -f "$tidy_errors"
  exit 1
fi
rm -f "$tidy_errors"
if [[ -n "$tidy_diff" ]]; then
  echo "go.mod / go.sum drift detected; run go mod tidy and commit the result." >&2
  printf '%s\n' "$tidy_diff" >&2
  exit 1
fi

echo "== go build =="
go build ./...

echo "== module graph =="
mod_snapshot="$(mktemp)"

go list -m -json all > "$mod_snapshot"
if ! grep -q '"Path": "github.com/dmundt/go-cask"' "$mod_snapshot"; then
  echo "module graph is empty or malformed; inspect go list -m -json all" >&2
  exit 1
fi

echo "== go vet =="
go vet ./...

echo "== import boundary check =="
if grep -RInE 'dmundt/go-cask/examples|dmundt/go-cask/gitlike' cas internal cmd >/dev/null 2>&1; then
  echo "cas/, internal/, and cmd/ must not import examples/ or the gitlike app layer." >&2
  exit 1
fi

echo "== gitlike codec guard =="
if go list -deps ./gitlike | grep -E 'cas/codec' >/dev/null 2>&1; then
  echo "gitlike must not depend on the codec layer; inject codecs via gitlike.Codecs." >&2
  exit 1
fi

echo "== govulncheck =="
if [[ "${VERIFY_SKIP_SECURITY:-false}" == "true" ]]; then
  echo "skipped (run by the separate CI security job)"
else
  ./scripts/security.sh
fi

echo "== test -race + coverage gate =="
fail=0

# One table, one authority: "<threshold>|<package>|<tier>". Tier 90 holds the
# foundational storage and reference packages plus everything on the default
# write or verification path; tier 80 holds the supporting packages that are
# part of the shipped surface. The tier name in column three is documentation
# only: the number in column one is what the gate enforces, and
# docs/specs/testing-strategy.md §5 defines the rule instead of restating this
# list.
coverage_targets=(
  # Tier 90.
  "90|./cas|core"
  "90|./cas/backend|backend"
  "90|./cas/backend/fs|backend"
  "90|./cas/backend/mem|backend"
  "90|./cas/backend/snapshot|backend"
  "90|./cas/repo|reference"
  "90|./cas/refs|reference"
  "90|./cas/pack|reference"
  "90|./cas/codec/flate|codec"
  "90|./cas/bloom/persistent|index"
  "90|./internal/store|seam"
  # Tier 80.
  "80|./cas/backend/packfs|backend"
  "80|./cas/bloom|index"
  "80|./cas/bloom/counting|index"
  "80|./cas/bloom/standard|index"
  "80|./cas/cache|support"
  "80|./cas/cache/mem|support"
  "80|./cas/cache/lru|support"
  "80|./cas/cache/prefetch|support"
  "80|./cas/codec/binary|codec"
  "80|./cas/codec/cbor|codec"
  "80|./cas/codec/gob|codec"
  "80|./cas/codec/gzip|codec"
  "80|./cas/codec/json|codec"
  "80|./cas/codec/zlib|codec"
  "80|./cas/hash|hash"
  "80|./cas/hash/sha256|hash"
  "80|./cas/hash/sha512|hash"
  "80|./cas/hash/sha512_256|hash"
  "80|./cas/verify/adler32|verify"
  "80|./cas/verify/crc32|verify"
  "80|./cas/verify/crc64|verify"
  "80|./gitlike|reference"
  "80|./internal/index|support"
  "80|./internal/web|viewer"
  "80|./cmd/cask|command"
)
# Deliberately ungated packages, as "<package>|<reason>". The coverage tier
# check below compares this register against go list ./cas/... , so a package
# can only be ungated as a written decision, never by omission. It is empty:
# every package under cas/ carries a numeric gate.
coverage_exempt=()

echo "== coverage tier check =="
# The gate is an enumeration, so it can silently fall behind the tree: the cas/
# packages added since the list was last touched were measured by nothing, and a
# reader could not tell an exemption from an omission. This check makes both
# loud. `go list ./cas/...` is the authority on which packages exist, and every
# one of them must carry a numeric tier or an entry in coverage_exempt with a
# written reason, so a package that appears in neither fails here instead of
# passing unnoticed.
go list ./cas/... \
  | sed 's|^github.com/dmundt/go-cask/||' \
  | sort > "$cas_packages"
{
  for target in "${coverage_targets[@]}"; do
    printf '%s\n' "$target" | cut -d'|' -f2 | sed 's|^\./||'
  done
  for exempt in ${coverage_exempt[@]+"${coverage_exempt[@]}"}; do
    printf '%s\n' "$exempt" | cut -d'|' -f1 | sed 's|^\./||'
  done
} | sort -u > "$tiered_packages"
missing="$(comm -23 "$cas_packages" "$tiered_packages")"
if [[ -n "$missing" ]]; then
  echo "every package under cas/ needs a coverage tier in scripts/verify.sh:" >&2
  printf '%s\n' "$missing" >&2
  echo 'add "<threshold>|./<package>|<tier>" or "<package>|<reason>" to coverage_exempt.' >&2
  exit 1
fi

for target in "${coverage_targets[@]}"
 do
  IFS='|' read -r threshold pkg tier <<< "$target"
  out="$(go test -race -cover "$pkg" 2>&1)"
  echo "$out"
  cov="$(printf '%s\n' "$out" | grep -oE 'coverage: [0-9.]+%' | tail -n 1 | sed 's/^coverage: //; s/%$//')" || true
  if [[ -z "$cov" ]]; then
    echo "coverage output missing for ${pkg}" >&2
    fail=1
  elif ! awk -v c="$cov" -v threshold="$threshold" 'BEGIN { exit !(c + 0 >= threshold) }'; then
    echo "coverage ${cov}% below ${threshold}% for ${pkg}" >&2
    fail=1
  fi
done

go test -race ./...
if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "== fuzz smoke =="
go test -run=^$ -fuzz=FuzzParseDigest -fuzztime=5s ./cas/
go test -run=^$ -fuzz=FuzzPathRoundTrip -fuzztime=5s ./cas/backend/fs/
go test -run=^$ -fuzz=FuzzVerify -fuzztime=5s ./cas/backend/fs/
go test -run=^$ -fuzz=FuzzCodecRoundTrip -fuzztime=5s ./cas/codec/json/

echo "== helper script behaviour =="
./scripts/test-bench-scripts.sh
./scripts/test-land-lane.sh
./scripts/test-version-fields.sh

fi # scope == full

echo "== doc integrity =="
# Mermaid balance is checked in every tracked Markdown file, not only the specs
# folder: AGENTS.md, the READMEs, website/ and benchmarks/ render diagrams too,
# and §9 of docs/specs/AGENT.md applies to all of them.
fail_doc=0
while IFS= read -r f; do
  m="$(grep -c '^```mermaid$' "$f" || true)"
  c="$(grep -c '^```$' "$f" || true)"
  if [[ "$c" -lt "$m" ]]; then
    echo "unbalanced mermaid in $f" >&2
    fail_doc=1
  fi
done < <(git ls-files '*.md')
python3 - "$repo_root" <<'PY'
import pathlib
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

repo_root = pathlib.Path(sys.argv[1]).resolve()
inline = re.compile(
    r'(?<!!)\[[^\]]+\]\(\s*(?:<(?P<angled>[^>]+)>|(?P<target>[^\s)]+))'
)
reference = re.compile(r'^\s*\[[^\]]+\]:\s*(?P<target>\S+)', re.MULTILINE)
html = re.compile(
    r'<!--|</?(?:a|abbr|address|article|aside|audio|blockquote|body|button|'
    r'canvas|caption|cite|code|col|data|dd|del|details|dfn|dialog|div|dl|dt|'
    r'em|embed|fieldset|figcaption|figure|footer|form|h[1-6]|head|header|'
    r'hgroup|hr|html|iframe|img|input|ins|kbd|label|legend|li|link|main|map|'
    r'mark|menu|meta|meter|nav|noscript|object|ol|optgroup|option|output|p|'
    r'picture|pre|progress|q|s|samp|script|section|select|small|source|span|'
    r'style|sub|summary|sup|table|tbody|td|template|textarea|tfoot|th|thead|'
    r'time|title|tr|track|u|ul|var|video|wbr)(?:\s[^<>]*)?/?>',
)
errors = []
files = subprocess.check_output(
    ['git', 'ls-files', '*.md'], cwd=repo_root, text=True
).splitlines()


def strip_code(lines):
    """Blank out fenced blocks and inline code spans.

    Prose rules do not apply to code: a Go generic call such as
    `New[T](encode, decode)` is shaped exactly like a Markdown link, and a
    sample or an inline mention of a tag is not raw HTML in the document.
    Lines are blanked rather than dropped so reported positions stay honest.
    """
    out = []
    fence = None
    for line in lines:
        marker = re.match(r'^\s*(```+|~~~+)', line)
        if fence is None and marker:
            fence = marker.group(1)[0] * 3
            out.append(line)
            continue
        if fence is not None:
            if marker and marker.group(1).startswith(fence):
                fence = None
            else:
                out.append('')
                continue
            out.append(line)
            continue
        out.append(re.sub(r'`+[^`]*`+', '', line))
    return out


for filename in sorted(files):
    path = repo_root / filename
    raw_lines = path.read_text(encoding='utf-8', errors='ignore').splitlines()
    lines = strip_code(raw_lines)
    text = '\n'.join(lines)
    for match in html.finditer(text):
        errors.append(f'{filename}: raw HTML is not allowed: {match.group(0)}')
    for line in lines:
        if re.match(r'^\s*(?:```|~~~)\s*(?:html|xml|svg)\b', line, re.I):
            errors.append(f'{filename}: HTML/XML/SVG code fences are not allowed')
    for match in list(inline.finditer(text)) + list(reference.finditer(text)):
        groups = match.groupdict()
        target = (groups.get('angled') or groups.get('target') or '').strip()
        parsed = urlsplit(target)
        if not parsed.path or parsed.scheme or parsed.netloc or target.startswith('#'):
            continue
        if parsed.path.startswith('/'):
            target_path = repo_root / unquote(parsed.path.lstrip('/'))
        else:
            target_path = path.parent / unquote(parsed.path)
        if not target_path.exists():
            errors.append(f'{filename}: {target}')
# CHANGELOG.md is published on the website and read by scripts/release-notes.sh,
# so its own structure is checked here rather than left to review (AGENTS.md,
# "Changelog and release-note policy"). A release heading without a link
# definition renders as literal bracket text instead of a link; a heading
# repeated inside one release section merges two sections into one, which is how
# three `### Security` blocks accumulated in `Unreleased`; and an `[Unreleased]`
# range that still starts at an older tag folds already-released versions back
# into "unreleased" and reports the wrong compare range for the next release.
changelog_head = re.compile(r'^## \[(?P<label>[^\]]+)\]')
changelog_section = re.compile(r'^###\s+(?P<title>Added|Changed|Deprecated|Removed|Fixed|Security)\s*$')
changelog_link = re.compile(r'^\[(?P<label>[^\]]+)\]:\s*\S')
changelog_unreleased = re.compile(r'^\[Unreleased\]:\s*\S*?compare/(?P<base>[^\s]+?)\.\.\.HEAD\s*$')
changelog_lines = (repo_root / 'CHANGELOG.md').read_text(
    encoding='utf-8', errors='ignore'
).splitlines()
defined = {
    match.group('label')
    for match in (changelog_link.match(line) for line in changelog_lines)
    if match
}
release = None
newest_release = None
unreleased = None
seen = set()
for number, line in enumerate(changelog_lines, 1):
    compare = changelog_unreleased.match(line)
    if compare and unreleased is None:
        unreleased = (number, compare.group('base'))
    heading = changelog_head.match(line)
    if heading:
        release = heading.group('label')
        seen = set()
        if newest_release is None and release != 'Unreleased':
            newest_release = release
        if release not in defined:
            errors.append(
                f'CHANGELOG.md:{number}: release heading [{release}] has no '
                f'[{release}]: link definition'
            )
        continue
    section = changelog_section.match(line)
    if section and release is not None:
        if section.group('title') in seen:
            errors.append(
                f'CHANGELOG.md:{number}: ### {section.group("title")} repeats '
                f'in the {release} section'
            )
        seen.add(section.group('title'))
if unreleased and newest_release and unreleased[1] != newest_release:
    errors.append(
        f'CHANGELOG.md:{unreleased[0]}: [Unreleased] compares from '
        f'{unreleased[1]}, but the newest released section is {newest_release}'
    )
for error in sorted(set(errors)):
    print(f'Markdown integrity error: {error}', file=sys.stderr)
if errors:
    sys.exit(1)
PY
cd "$repo_root"
if [[ "$fail_doc" -ne 0 ]]; then
  exit 1
fi

echo "== website footer =="
# The published footer is one line, composed by website/macros.py from the
# checked-out revision; nothing else in the gate evaluates it. The module's own
# self-test composes that line for fixed inputs and exits non-zero on a changed
# rendered text, a returning zone label or second line, a config that lost the
# line, or a date that was guessed instead of omitted.
python3 website/macros.py --selftest

echo "== website examples =="
# Every Go fence on the site is a complete unit (website/AGENT.md, "Examples
# and links"): the extraction below writes each one into its own package under
# .gocache/website-examples and the whole set is then built and vetted. The
# same pass asserts the shipped-package inventory tables in website/concepts/
# against the tree, so a new backend, hasher or codec cannot ship undocumented.
# This section is deliberately standalone: it shares no state with the
# coverage or doc-integrity steps and can be moved without touching either.
webdir="$repo_root/.gocache/website-examples"
python3 - "$repo_root" <<'PY'
import pathlib
import re
import shutil
import sys

repo_root = pathlib.Path(sys.argv[1]).resolve()
website = repo_root / 'website'
webdir = repo_root / '.gocache' / 'website-examples'

shutil.rmtree(webdir, ignore_errors=True)
webdir.mkdir(parents=True, exist_ok=True)

fence_line = re.compile(r'^\s*(`{3,}|~{3,})\s*(.*?)\s*$')
errors = []
materialized = []
pages = 0


def go_blocks(lines):
    """Yield (line number, info string, body lines) for every Go fence."""
    marker = None
    start = 0
    info = ''
    body = []
    for number, line in enumerate(lines, 1):
        match = fence_line.match(line)
        if marker is None:
            if match:
                marker = match.group(1)[0] * 3
                start = number
                info = match.group(2)
                body = []
            continue
        if match and match.group(1).startswith(marker):
            if info.split(' ', 1)[0] == 'go':
                yield start, info, body
            marker = None
            continue
        body.append(line)


for page in sorted(website.rglob('*.md')):
    pages += 1
    relative = page.relative_to(website).as_posix()
    lines = page.read_text(encoding='utf-8', errors='ignore').splitlines()
    index = 0
    for number, info, body in go_blocks(lines):
        index += 1
        if info != 'go':
            errors.append(
                f'{relative}:{number}: a Go fence must use the info string '
                f'"go" alone, not "{info}"'
            )
            continue
        first = next((line for line in body if line.strip()), '')
        if not first.startswith('package '):
            errors.append(
                f'{relative}:{number}: Go block is not a complete unit; every '
                'Go block must declare its own package clause and imports '
                '(website/AGENT.md, "Examples and links")'
            )
            continue
        slug = relative[:-3].replace('/', '-')
        directory = webdir / f'{slug}-{index}'
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'example.go').write_text(
            '\n'.join(body) + '\n', encoding='utf-8', newline='\n'
        )
        materialized.append(f'{relative}:{number}')

# The shipped-package inventory tables are the site's claim about what ships.
# Compare each one with the packages that exist, so a new backend, hasher or
# codec cannot ship without a matching table row (and a removed one cannot
# linger).
inventories = (
    ('concepts/backends.md', 'cas/backend'),
    ('concepts/hashes.md', 'cas/hash'),
    ('concepts/codecs.md', 'cas/codec'),
    ('specifications/object-format.md', 'cas/codec'),
)
for relative, root in inventories:
    page = website / relative
    if not page.is_file():
        errors.append(f'{relative}: inventory page is missing')
        continue
    shipped = set()
    for child in sorted((repo_root / root).iterdir()):
        if child.is_dir() and any(p.suffix == '.go' for p in child.iterdir()):
            shipped.add(f'{root}/{child.name}')
    documented = set()
    token = re.compile(r'`(' + re.escape(root) + r'/[A-Za-z0-9_]+)`')
    for line in page.read_text(encoding='utf-8', errors='ignore').splitlines():
        if line.lstrip().startswith('|'):
            documented.update(token.findall(line))
    missing = sorted(shipped - documented)
    extra = sorted(documented - shipped)
    if missing:
        errors.append(f'{relative}: inventory table is missing {", ".join(missing)}')
    if extra:
        errors.append(
            f'{relative}: inventory table names packages that do not exist: '
            f'{", ".join(extra)}'
        )

for error in errors:
    print(f'Website example error: {error}', file=sys.stderr)
if errors:
    sys.exit(1)
print(f'materialized {len(materialized)} Go blocks from {pages} Markdown pages')
print('shipped-package inventory tables match the tree')
PY
if ! (cd "$webdir" && go build ./... && go vet ./...); then
  echo "website Go examples failed to build or vet; materialized copies are in $webdir" >&2
  exit 1
fi
rm -rf "$webdir"

if [[ -n "${CASK_RELEASE_TAG:-}" ]]; then
  echo "== release note sync =="
  ./scripts/release-notes.sh "$CASK_RELEASE_TAG" "${CASK_RELEASE_FROM_TAG:-}" > /tmp/cask-release-notes.txt
  grep -q 'Full Changelog:' /tmp/cask-release-notes.txt
fi

# Record the green run for this exact commit in the shared git dir, so
# .githooks/pre-push accepts the push without re-running a gate whose tree has
# not changed. Entries are keyed by commit, so amending or rebasing invalidates
# one automatically.
#
# The ledger is appended, not overwritten: every worktree of this clone shares
# the file, and a single-slot stamp let a gate run in one worktree invalidate
# another branch's verified commit and refuse its push. The file stays bounded
# by keeping the newest entries; the same commit re-verified replaces its line.
stamp_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
if [[ -n "$stamp_dir" ]]; then
  head_sha="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  entry="$(printf '%s %s %s' "$head_sha" "$scope" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")"
  {
    if [[ -f "$stamp_dir/verify.ok" ]]; then
      grep -v "^$head_sha " "$stamp_dir/verify.ok" || true
    fi
    printf '%s\n' "$entry"
  } | tail -n 200 >"$stamp_dir/verify.ok.tmp" &&
    mv "$stamp_dir/verify.ok.tmp" "$stamp_dir/verify.ok"
fi

echo "verification passed"
