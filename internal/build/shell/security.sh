#!/usr/bin/env bash
# ARCHIVED — superseded by `go run ./cmd/buildtool security`.
#
# This script installed the pinned govulncheck release and ran it. The pin now lives
# once, in internal/build/policy; where the binary lands and whether the installed one
# is the pinned release are internal/build/toolchain's decisions; and the gate and CI
# both run the command above. The archive also records a case the port fixed: this
# script translated every GOBIN to the WSL /mnt form, which on a native Windows run
# installs the scanner into a directory nothing looks in — the command translates only
# inside WSL. Nothing runs this copy; see ../shell/README.md.
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
