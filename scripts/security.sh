#!/usr/bin/env bash
set -euo pipefail

govulncheck_version="${GOVULNCHECK_VERSION:-v1.8.0}"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required for the security gate" >&2
  exit 1
fi

gobin="${GOBIN:-$(go env GOBIN 2>/dev/null || true)}"
if [[ -z "$gobin" ]]; then
  gobin="$(go env GOPATH)/bin"
fi
gobin="$(printf '%s' "$gobin" | sed 's|\\|/|g')"
case "$gobin" in
  [A-Za-z]:*)
    drive="${gobin%%:*}"
    rest="${gobin#*:}"
    gobin="/mnt/${drive,,}${rest}"
    ;;
esac
mkdir -p "$gobin"

govulncheck_bin="$gobin/govulncheck"
if [[ -x "$gobin/govulncheck.exe" ]]; then
  govulncheck_bin="$gobin/govulncheck.exe"
fi
if [[ ! -x "$govulncheck_bin" ]] ||
  ! "$govulncheck_bin" -version 2>/dev/null |
    grep -q "Scanner: govulncheck@${govulncheck_version}$"; then
  GOBIN="$gobin" go install "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}"
fi

if [[ -x "$gobin/govulncheck.exe" ]]; then
  govulncheck_bin="$gobin/govulncheck.exe"
fi
if [[ ! -x "$govulncheck_bin" ]]; then
  echo "govulncheck was not installed to $gobin" >&2
  exit 1
fi

"$govulncheck_bin" ./...
