package depgraph

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goList asks Go for the module path and every package's local import data, which
// is the input the graph is derived from. It is shared by the tests here rather
// than duplicated, and it fails loudly when the toolchain or the module is not
// available so a test cannot silently pass on an empty graph.
//
// dir is the repository root: `./...` is resolved against the working directory,
// so running it from this package's own directory would list one package and the
// comparison below would prove nothing.
func goList(t *testing.T, dir string) (string, []Package) {
	t.Helper()

	moduleOut, err := goListIn(dir, "-m", "-f", "{{.Path}}")
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	module := strings.TrimSpace(moduleOut)
	if module == "" {
		t.Fatal("go list -m reported an empty module path")
	}

	rowsOut, err := goListIn(dir, "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./...")
	if err != nil {
		t.Fatalf("go list ./...: %v", err)
	}

	var packages []Package
	for _, line := range strings.Split(strings.TrimSuffix(rowsOut, "\n"), "\n") {
		if line == "" {
			continue
		}
		path, imports, _ := strings.Cut(line, "|")
		if path == "" {
			t.Fatalf("go list printed a row without a package path: %q", line)
		}
		packages = append(packages, Package{ImportPath: path, Imports: strings.Fields(imports)})
	}
	if len(packages) == 0 {
		t.Fatal("go list ./... reported no packages, so the test would prove nothing")
	}
	return module, packages
}

// goListIn runs one `go list` invocation with its working directory set, so a
// caller can ask about the module rather than about this package. The directory is
// made absolute because a command's working directory must be: a relative one
// ("../..") is resolved against the test binary's own directory and the toolchain
// then reports no module.
func goListIn(dir string, args ...string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	full := append([]string{"list"}, args...)
	cmd := exec.Command("go", full...)
	cmd.Dir = absolute
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s (dir %s): %w: %s",
			strings.Join(full, " "), absolute, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
