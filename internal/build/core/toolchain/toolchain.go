// Package toolchain resolves the external tools a gate runs: where a `go install`ed
// binary lands, which version a scanner reports for itself, and how a Windows path
// reads from inside WSL.
//
// Nothing here installs or runs anything. The decisions — GOBIN wins over
// GOPATH/bin, an installed scanner counts only when its report names the pinned
// version, a Windows path becomes a /mnt path — are pure functions over strings, so
// they are testable without a toolchain, and the caller owns the install and the run.
package toolchain

import "strings"

// NeedsMount reports whether a path a Go toolchain reported has to be translated to
// the platform this process runs on. That is true only inside WSL — a Linux process
// under a Windows kernel, where the `go` on PATH may still be the Windows one — and
// the caller passes what it read: `runtime.GOOS` and the kernel release. A native
// Windows run needs no translation, and applying one there is how a tool ends up
// installed into a directory nothing looks in.
func NeedsMount(goos, osrelease string) bool {
	if goos != "linux" {
		return false
	}
	release := strings.ToLower(osrelease)
	return strings.Contains(release, "microsoft") || strings.Contains(release, "wsl")
}

// BinDir returns the directory a `go install`ed tool lands in: GOBIN when it is set,
// otherwise the bin directory of GOPATH. Both values come from the caller — the
// environment, or `go env` — so the rule is testable without a Go toolchain.
func BinDir(gobin, gopath string) string {
	if gobin != "" {
		return gobin
	}
	if gopath == "" {
		return ""
	}
	return strings.TrimRight(gopath, `/\`) + "/bin"
}

// MountPath translates a Windows path to the form the same directory has inside WSL,
// where this repository's gate runs on a Windows checkout: `C:\Users\me\go\bin` and
// `C:/Users/me/go/bin` both become `/mnt/c/Users/me/go/bin`, because `go env` there
// answers with the Windows form while the binary still has to be found by path. A
// path that is already a POSIX path is returned with its separators normalised.
func MountPath(value string) string {
	normalized := strings.ReplaceAll(value, `\`, "/")
	// A drive letter is one letter followed by a colon: "C:/..." but not "go/bin",
	// and not the "[x]:" a caller might pass.
	if len(normalized) >= 2 && normalized[1] == ':' && isDriveLetter(normalized[0]) {
		return "/mnt/" + strings.ToLower(normalized[:1]) + normalized[2:]
	}
	return normalized
}

// isDriveLetter reports whether b is an ASCII letter, which is all a Windows drive
// letter can be.
func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// Candidates returns the file names a tool may have in one bin directory, in the
// order to look for them. The Windows executable comes first: a shared bin directory
// is written by whichever toolchain ran `go install`, and on a Windows checkout driven
// from WSL that is the Windows one, which appends `.exe`. A Linux-only bin directory
// has neither form first, and the plain name is found either way.
func Candidates(name string) []string {
	return []string{name + ".exe", name}
}

// ScannerVersion returns the version a tool's `-version` report names for itself: the
// value after `Scanner: <name>@` on the line that carries it. It returns "" when the
// report names no such scanner, which is what an unrelated binary, an older tool or a
// report in another shape prints — a caller then installs rather than trusting it.
func ScannerVersion(report, name string) string {
	marker := "Scanner: " + name + "@"
	for _, line := range strings.Split(report, "\n") {
		if after, found := strings.CutPrefix(strings.TrimSpace(line), marker); found {
			return after
		}
	}
	return ""
}

// Pinned reports whether the installed tool is the pinned one: its report names
// exactly the wanted version. Another version, or a report that names none, is a tool
// the gate did not choose, and it is installed rather than accepted.
func Pinned(report, name, version string) bool {
	return version != "" && ScannerVersion(report, name) == version
}
