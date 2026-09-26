# Toolchain resolution, sourced by the entry points that start the build tool.
#
# Git Bash and WSL may not inherit Go's installation path, so a shim that needs `go` finds it
# first: ask the Windows toolchain where it is when the POSIX shell cannot see it, then look
# in the usual places, then next to whichever `go` it did find.
#
# It is sourced rather than executed because PATH can only be changed in the caller's shell,
# and it is POSIX sh on purpose: `.githooks/pre-push` runs under `sh`. It sets PATH and
# CGO_ENABLED and defines nothing a caller relies on.
#
# It ships no rule of its own: which toolchain a checkout is worked by is the host's
# business, and every decision the build tool makes lives in cmd/buildtool.

if ! command -v go >/dev/null 2>&1 || ! command -v gofmt >/dev/null 2>&1; then
  if command -v powershell.exe >/dev/null 2>&1; then
    if ! command -v go >/dev/null 2>&1; then
      win_go="$(powershell.exe -NoProfile -Command "(Get-Command go -ErrorAction Stop).Source" 2>/dev/null | tr -d '\r' | head -n 1 || true)"
      if [ -n "$win_go" ]; then
        PATH="$(dirname "$win_go"):$PATH"
      fi
    fi
    if ! command -v gofmt >/dev/null 2>&1; then
      win_gofmt="$(powershell.exe -NoProfile -Command "(Get-Command gofmt -ErrorAction Stop).Source" 2>/dev/null | tr -d '\r' | head -n 1 || true)"
      if [ -n "$win_gofmt" ]; then
        PATH="$(dirname "$win_gofmt"):$PATH"
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
  if [ -d "$candidate" ]; then
    PATH="$candidate:$PATH"
  fi
done

if command -v go >/dev/null 2>&1 && ! command -v gofmt >/dev/null 2>&1; then
  go_bin="$(dirname "$(command -v go)")"
  if [ -x "$go_bin/gofmt" ] || [ -x "$go_bin/gofmt.exe" ]; then
    PATH="$go_bin:$PATH"
  fi
fi

export PATH
export CGO_ENABLED="${CGO_ENABLED:-1}"
