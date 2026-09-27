package toolchain

import (
	"strings"
	"testing"
)

// FuzzMountPath checks the translation a Windows checkout driven from WSL depends on: it
// never panics, it is idempotent — translating an already translated path must not
// translate it again — and a drive path lands under the lower-case mount point of its
// drive.
//
// A drive-relative path (`C:foo` is a path on the current directory of drive C:) has no
// separator to preserve, so the property is the prefix mapping rather than a shape: the
// translation is the mount point plus whatever followed the colon.
func FuzzMountPath(f *testing.F) {
	f.Add(`C:\Users\me\go\bin`)
	f.Add("D:/src/go/bin")
	f.Add("/home/me/go/bin")
	f.Add("go/bin")
	f.Add("")
	f.Add("C:")
	f.Add(`\\server\share`)
	f.Add("C:relative")

	f.Fuzz(func(t *testing.T, value string) {
		once := MountPath(value)
		if twice := MountPath(once); twice != once {
			t.Fatalf("MountPath(%q) = %q, then %q", value, once, twice)
		}
		if strings.Contains(once, `\`) {
			t.Fatalf("MountPath(%q) = %q, which still carries a backslash", value, once)
		}
		normalized := strings.ReplaceAll(value, `\`, "/")
		if len(normalized) >= 2 && normalized[1] == ':' && isDriveLetter(normalized[0]) {
			want := "/mnt/" + strings.ToLower(normalized[:1]) + normalized[2:]
			if once != want {
				t.Fatalf("MountPath(%q) = %q, want %q", value, once, want)
			}
		}
	})
}

// FuzzScannerVersion checks the report reader the pinned-tool decision rests on: it never
// panics, a version it reports is the text after the marker rather than the whole line,
// and a report whose value is a clean token pins itself — a reader that could not do that
// would reinstall a tool that is already the pinned one.
func FuzzScannerVersion(f *testing.F) {
	f.Add("Scanner: govulncheck@v1.8.0\nGo: go1.27.1\n")
	f.Add("")
	f.Add("Scanner: govulncheck@")
	f.Add("Scanner:govulncheck@v1\n")
	f.Add("scanner: govulncheck@v9\n")
	f.Add("Scanner: govulncheck@ v1\n")

	f.Fuzz(func(t *testing.T, report string) {
		version := ScannerVersion(report, "govulncheck")
		if version == "" {
			return
		}
		if strings.ContainsAny(version, "\n") {
			t.Fatalf("ScannerVersion(%q) returned a multi-line value %q", report, version)
		}
		if strings.Contains(version, "Scanner: ") {
			t.Fatalf("ScannerVersion(%q) returned %q, which is the whole line", report, version)
		}
		// The value is read verbatim, so only one that is already a clean token can be
		// the pinned one; a report with stray whitespace is a report to reinstall from.
		if strings.TrimSpace(version) != version {
			return
		}
		if !Pinned(report, "govulncheck", version) {
			t.Fatalf("Pinned(%q, %q) is false for the version the report names", report, version)
		}
	})
}
