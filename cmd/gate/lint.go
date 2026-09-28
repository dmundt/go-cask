package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/toolchain"
)

// runLint installs the pinned static analyzer when the installed one is not it, and runs
// it over the module against the repository's committed configuration.
//
// The layer matrix is not this command's: `layer-matrix` owns it, and the depguard block in
// the configuration only mirrors it so a violation surfaces in an editor rather than after
// a whole gate run.
func runLint(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("lint", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	linter := policy.Linter()
	version := os.Getenv(linter.VersionEnv)
	if version == "" {
		version = linter.Version
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	binDir, err := scannerBinDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", binDir, err)
	}

	bin, err := installedLinter(binDir, linter.Name, version)
	if err != nil {
		return err
	}
	if bin == "" {
		fmt.Fprintf(out, "installing %s@%s into %s\n", linter.Package, version, binDir)
		install := exec.Command("go", "install", linter.Package+"@"+version)
		install.Dir = root
		install.Env = envWith("GOBIN", binDir)
		install.Stdout, install.Stderr = out, errOut
		if err := install.Run(); err != nil {
			return fmt.Errorf("go install %s@%s: %w", linter.Package, version, err)
		}
		if bin, err = installedLinter(binDir, linter.Name, version); err != nil {
			return err
		}
		if bin == "" {
			return fmt.Errorf("%s was not installed into %s", linter.Name, binDir)
		}
	}

	run := exec.Command(bin, "run", "--config", linter.Config, "./...")
	run.Dir = root
	run.Stdout, run.Stderr = out, errOut
	if err := run.Run(); err != nil {
		return fmt.Errorf("%s run --config %s ./...: %w", linter.Name, linter.Config, err)
	}
	return nil
}

// installedLinter returns the path to the pinned analyzer in binDir, or "" when that
// directory holds no binary whose report names the pinned release. The pinned release is
// compared ignoring the `v` prefix, because the analyzer prints the plain version it was
// built as while the pin is the tag it was installed from.
func installedLinter(binDir, name, version string) (string, error) {
	for _, candidate := range toolchain.Candidates(name) {
		path := filepath.Join(binDir, candidate)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		report, err := toolOutput(path, "--version")
		if err != nil {
			continue
		}
		if toolchain.PinnedRelease(report, name, version) {
			return path, nil
		}
	}
	return "", nil
}
