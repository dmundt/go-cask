---
type: Guide
title: toolchain (build engine) — go-cask
description: Resolving gate tools — bin directory, WSL path translation, pinned-release check.
version: v3
---

# toolchain

Resolving the external tools a gate runs, without running them.

- `BinDir(gobin, gopath)` → where a `go install`ed tool lands: `GOBIN` when set, else
  `GOPATH/bin`.
- `NeedsMount(goos, osrelease)` → must a Go-reported path be translated: true only inside
  WSL, where `go` on PATH may still be the Windows one → a native Windows or Linux run keeps
  the path it was given.
- `MountPath` → the translation: `C:\Users\me\go\bin` → `/mnt/c/Users/me/go/bin`.
- `Candidates` → the file names a tool may have in one bin directory, Windows executable
  first; a shared bin directory is written by whichever toolchain ran the install.
- `ScannerVersion(report, name)` → the version a tool's `-version` report names for itself:
  the value after `Scanner: <name>@`.
- `Pinned(report, name, version)` → an installed tool counts only when its report names
  exactly the pinned version → an older or unrelated binary is installed over, not trusted.

Pure over strings: no install, no exec, no filesystem. Caller owns the `go install`, the
`exec`, the files.

## Testing

`go test ./toolchain/` — every bin-directory rule, the WSL decision and drive-path
translation, the candidate order, the version extraction, the pinned decision.
