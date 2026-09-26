package policy

import (
	"strings"
	"testing"
)

// TestSecurityToolIsComplete pins the table's own shape: the install target, the
// pinned release and the override's name are all there, and the overriding variable
// is spelled as a constant rather than left empty.
func TestSecurityToolIsComplete(t *testing.T) {
	t.Parallel()

	scanner := Scanner()
	if scanner.Name == "" || scanner.Package == "" || scanner.Version == "" || scanner.VersionEnv == "" {
		t.Fatalf("the scanner table is incomplete: %+v", scanner)
	}
	if !strings.HasPrefix(scanner.Version, "v") {
		t.Errorf("pinned version %q is not a Go module version", scanner.Version)
	}
	if !strings.HasSuffix(scanner.Package, "/"+scanner.Name) {
		t.Errorf("package %q does not end in the executable's name %q", scanner.Package, scanner.Name)
	}
}

// TestSecurityPinLivesOnlyHere guards the one-place rule the table exists for: the
// workflow runs the scanner through the command, and a second copy of the pinned
// version in the workflow is the drift this table removed.
func TestSecurityPinLivesOnlyHere(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, repoRoot(t), ciWorkflowPath)
	if !strings.Contains(workflow, "buildtool security") {
		t.Errorf("%s does not run the scanner through `go run ./cmd/buildtool security`", ciWorkflowPath)
	}
	if strings.Contains(workflow, Scanner().Version) {
		t.Errorf("%s restates the pinned scanner version %s; the pin lives in internal/build/policy",
			ciWorkflowPath, Scanner().Version)
	}
}
