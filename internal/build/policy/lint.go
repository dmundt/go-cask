package policy

// LintTool is the static analyzer the gate runs: the executable's name, the package
// `go install` takes, the release the repository pins, and the environment variable that
// overrides the pin for an ad-hoc run. Config is the repository-relative file the tool is
// pointed at, so a run reads the settings this repository committed rather than whatever a
// home directory happens to hold.
//
// The pin lives here and nowhere else, for the same reason the scanner's does: a lint run
// that is not pinned is a run whose result cannot be compared with the last one.
type LintTool struct {
	// Name is the executable's file name, without the platform's suffix.
	Name string
	// Package is the import path of the command `go install` builds.
	Package string
	// Version is the pinned release, as `go install <package>@<version>` spells it.
	Version string
	// VersionEnv names the environment variable that overrides Version.
	VersionEnv string
	// Config is the repository-relative configuration file the tool reads.
	Config string
}

// Linter returns go-cask's pinned static analyzer. It is a function rather than a
// package-level variable so a caller cannot mutate the gate's policy by accident.
//
// The analyzer's own settings are `.golangci.yml`, which states why its depguard block
// mirrors the layer matrix rather than owning it.
func Linter() LintTool {
	return LintTool{
		Name:       "golangci-lint",
		Package:    "github.com/golangci/golangci-lint/v2/cmd/golangci-lint",
		Version:    "v2.14.0",
		VersionEnv: "GOLANGCI_LINT_VERSION",
		Config:     ".golangci.yml",
	}
}
