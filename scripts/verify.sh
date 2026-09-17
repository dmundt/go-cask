#!/usr/bin/env bash
set -euo pipefail

export CGO_ENABLED="${CGO_ENABLED:-1}"

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
go mod tidy
if ! git diff --exit-code -- go.mod go.sum >/dev/null 2>&1; then
  echo "go.mod / go.sum drift detected; run go mod tidy and commit the result." >&2
  git --no-pager diff -- go.mod go.sum || true
  exit 1
fi

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
if ! command -v go >/dev/null 2>&1; then
  echo "go is required for the security gate" >&2
  exit 1
fi

gobin="${GOBIN:-}"
if [[ -z "$gobin" ]]; then
  gobin="$(go env GOBIN 2>/dev/null || true)"
fi
if [[ -z "$gobin" ]]; then
  gobin="$(go env GOPATH 2>/dev/null || true)"
  if [[ -n "$gobin" ]]; then
    gobin="$gobin/bin"
  fi
fi
if [[ -z "$gobin" ]]; then
  gobin="$HOME/bin"
fi

gobin="$(printf '%s' "$gobin" | sed 's|\\|/|g')"
mkdir -p "$gobin"
export PATH="$gobin:$PATH"
if ! command -v govulncheck >/dev/null 2>&1; then
  GOBIN="$gobin" go install golang.org/x/vuln/cmd/govulncheck@latest
fi

if ! command -v govulncheck >/dev/null 2>&1; then
  echo "govulncheck was not installed to $gobin" >&2
  exit 1
fi
"$(command -v govulncheck)" ./...

echo "== test -race + coverage gate =="
fail=0
for pkg in \
  ./cas \
  ./cas/backend/fs \
  ./cas/backend/mem \
  ./cas/cache/mem \
  ./cas/cache/lru \
  ./cas/cache/prefetch \
  ./cas/codec/json \
  ./cas/codec/gob \
  ./cas/hash/sha256 \
  ./cas/hash/sha512_256 \
  ./gitlike \
  ./internal/index
 do
  out="$(go test -race -cover "$pkg" 2>&1)"
  echo "$out"
  cov="$(printf '%s\n' "$out" | grep -oE 'coverage: [0-9.]+%' | tail -n 1 | sed 's/^coverage: //; s/%$//')" || true
  if [[ -n "$cov" ]]; then
    awk -v c="$cov" 'BEGIN { if (c + 0 < 90.0) exit 1 }' || {
      echo "coverage ${cov}% below 90% for $pkg" >&2
      fail=1
    }
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
import sys

root = pathlib.Path(sys.argv[1]) / 'docs' / 'specs'
pat = re.compile(r'\[[^\]]+\]\((?P<target>[^)]+)\)|^\[[^\]]+\]:\s*(?P<target2>\S+)')
errors = []
for path in sorted(root.glob('*.md')):
    text = path.read_text(encoding='utf-8', errors='ignore')
    for match in pat.finditer(text):
        target = (match.group('target') or match.group('target2') or '').strip()
        if not target or target.startswith(('http://', 'https://', 'mailto:', '#')):
            continue
        target = target.split('#', 1)[0].split('?', 1)[0]
        if target.startswith('/'):
            target = root.parent.parent / target.lstrip('/')
        else:
            target = (path.parent / target)
        if not target.exists():
            errors.append(target.as_posix())
for ref in sorted(set(errors)):
    try:
        rel = pathlib.Path(ref).resolve().relative_to(root.parent.parent.resolve())
        label = rel.as_posix()
    except ValueError:
        label = pathlib.Path(ref).as_posix()
    print(f'broken reference: {label}', file=sys.stderr)
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
