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

if ! find . -name '*.go' -print -quit | grep -q .; then
  echo "No Go sources; verification skipped."
  exit 0
fi

echo "== gofmt =="
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "$unformatted"
  echo "gofmt needed; run gofmt -w ." >&2
  exit 1
fi

echo "== go mod tidy =="
tidy_diff="$(go mod tidy -diff 2>&1)" || {
  printf '%s\n' "$tidy_diff" >&2
  exit 1
}
if [[ -n "$tidy_diff" ]]; then
  echo "go.mod / go.sum drift detected; run go mod tidy and commit the result." >&2
  printf '%s\n' "$tidy_diff" >&2
  exit 1
fi

echo "== go build =="
go build ./...

echo "== module graph =="
mod_snapshot="$(mktemp)"
trap 'rm -f "$mod_snapshot"' EXIT

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
./scripts/security.sh

echo "== test -race + coverage gate =="
fail=0
coverage_targets=(
  "90|./cas"
  "90|./cas/backend/fs"
  "90|./cas/backend/mem"
  "80|./cas/cache/mem"
  "80|./cas/cache/lru"
  "80|./cas/cache/prefetch"
  "80|./cas/codec/json"
  "80|./cas/codec/gob"
  "80|./cas/hash/sha256"
  "80|./cas/hash/sha512_256"
  "80|./gitlike"
  "80|./internal/index"
)
for target in "${coverage_targets[@]}"
 do
  IFS='|' read -r threshold pkg <<< "$target"
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

echo "== doc integrity =="
cd docs/specs
fail_doc=0
for f in *.md; do
  m="$(grep -c '^```mermaid$' "$f" || true)"
  c="$(grep -c '^```$' "$f" || true)"
  if [[ "$c" -lt "$m" ]]; then
    echo "unbalanced mermaid in $f" >&2
    fail_doc=1
  fi
done
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
inline_code = re.compile(r'`[^`]*`')
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
for filename in sorted(files):
    path = repo_root / filename
    lines = path.read_text(encoding='utf-8', errors='ignore').splitlines()
    prose = []
    in_fence = False
    for line in lines:
        if line.lstrip().startswith(('```', '~~~')):
            in_fence = not in_fence
            continue
        if not in_fence:
            prose.append(line)
    text = inline_code.sub('', '\n'.join(prose))
    for match in html.finditer(text):
        errors.append(f'{filename}: raw HTML is not allowed: {match.group(0)}')
    for match in list(inline.finditer(text)) + list(reference.finditer(text)):
        target = (match.group('angled') or match.group('target') or '').strip()
        parsed = urlsplit(target)
        if not parsed.path or parsed.scheme or parsed.netloc or target.startswith('#'):
            continue
        if parsed.path.startswith('/'):
            target_path = repo_root / unquote(parsed.path.lstrip('/'))
        else:
            target_path = path.parent / unquote(parsed.path)
        if not target_path.exists():
            errors.append(f'{filename}: {target}')
for error in sorted(set(errors)):
    print(f'broken Markdown reference: {error}', file=sys.stderr)
if errors:
    sys.exit(1)
PY
cd "$repo_root"
if [[ "$fail_doc" -ne 0 ]]; then
  exit 1
fi

if [[ -n "${CASK_RELEASE_TAG:-}" ]]; then
  echo "== release note sync =="
  ./scripts/release-notes.sh "$CASK_RELEASE_TAG" "${CASK_RELEASE_FROM_TAG:-}" > /tmp/cask-release-notes.txt
  grep -q 'Full Changelog:' /tmp/cask-release-notes.txt
fi

echo "verification passed"
