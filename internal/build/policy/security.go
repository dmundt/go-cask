package policy

// SecurityTool is the vulnerability scanner the gate and CI run: the executable's
// name, the package `go install` takes, the release the repository pins, and the
// environment variable that overrides the pin for an ad-hoc run.
//
// The pin lives here and nowhere else. It used to be a shell default and a workflow
// environment variable, which is two copies of one decision, and a scan that runs
// un-pinned is a scan whose result cannot be compared with the last one.
type SecurityTool struct {
	// Name is the executable's file name, without the platform's suffix.
	Name string
	// Package is the import path of the command `go install` builds.
	Package string
	// Version is the pinned release, as `go install <package>@<version>` spells it.
	Version string
	// VersionEnv names the environment variable that overrides Version.
	VersionEnv string
}

// Scanner returns go-cask's pinned vulnerability scanner. It is a function rather
// than a package-level variable so a caller cannot mutate the gate's policy by
// accident.
func Scanner() SecurityTool {
	return SecurityTool{
		Name:       "govulncheck",
		Package:    "golang.org/x/vuln/cmd/govulncheck",
		Version:    "v1.8.0",
		VersionEnv: "GOVULNCHECK_VERSION",
	}
}
