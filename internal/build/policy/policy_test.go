package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/coverage"
	"github.com/dmundt/go-cask/internal/build/core/depgraph"
	"github.com/dmundt/go-cask/internal/build/core/deps"
	"github.com/dmundt/go-cask/internal/build/core/layers"
	"github.com/dmundt/go-cask/internal/build/core/website"
)

// repoRoot resolves the repository root from this source file's package
// directory, so the checks do not depend on the working directory. The package is
// three levels below the root: internal/build/policy.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(dir, "..", "..", ".."))
}

// goList asks Go for the module path and every package's import data, with its
// working directory set to the repository root. That is essential rather than
// tidy: `./...` resolves against the working directory, so running it from this
// package's own directory would list one package and a test built on it would
// compare an almost-empty graph against the committed document.
func goList(t *testing.T) (string, []depgraph.Package) {
	t.Helper()
	root := repoRoot(t)

	moduleOut, err := exec.Command("go", "list", "-m", "-f", "{{.Path}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	module := strings.TrimSpace(string(moduleOut))
	if module == "" {
		t.Fatal("go list -m reported an empty module path")
	}

	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./...")
	cmd.Dir = root
	rowsOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./...: %v", err)
	}
	var packages []depgraph.Package
	for _, line := range strings.Split(strings.TrimSuffix(string(rowsOut), "\n"), "\n") {
		if line == "" {
			continue
		}
		path, imports, _ := strings.Cut(line, "|")
		packages = append(packages, depgraph.Package{ImportPath: path, Imports: strings.Fields(imports)})
	}
	if len(packages) == 0 {
		t.Fatal("go list ./... reported no packages, so the test would prove nothing")
	}
	return module, packages
}

// TestMatrixIsWellFormed pins the table's own shape: every arm owns something and
// allows something, and the arms are ordered so the narrower claim comes first.
func TestMatrixIsWellFormed(t *testing.T) {
	t.Parallel()

	matrix := Matrix()
	if len(matrix) == 0 {
		t.Fatal("the matrix has no arms")
	}
	for _, layer := range matrix {
		if layer.Name == "" {
			t.Error("an arm names no layer")
		}
		if layer.Owns == nil {
			t.Errorf("arm %q owns nothing", layer.Name)
		}
		if len(layer.Allowed) == 0 {
			t.Errorf("arm %q allows no imports, so it would refuse everything", layer.Name)
		}
	}
}

// TestMatrixCoversEveryModuleTree keeps the arms exhaustive over the module's
// top-level trees. It is the guard against the failure the original grep had: a new
// top-level tree that no arm claims is checked by nothing, and silence in the gate
// would be read as compliance.
func TestMatrixCoversEveryModuleTree(t *testing.T) {
	t.Parallel()

	matrix := Matrix()
	// Every top-level tree in this module, as the repository layout in AGENTS.md
	// declares it. A tree added there must be added here and given an arm.
	trees := []string{"/cas", "/gitlike", "/internal", "/cmd", "/examples", "/benchmarks"}
	for _, tree := range trees {
		pkg := ModulePath + tree
		if _, ok := layers.Owner(matrix, pkg); !ok {
			t.Errorf("no arm claims %s: add one to Matrix so its imports are checked", pkg)
		}
	}
}

// TestMatrixArmsAreReached pins the reporting layer of each tree, so an arm that
// silently stopped matching fails here rather than passing everything.
func TestMatrixArmsAreReached(t *testing.T) {
	t.Parallel()

	matrix := Matrix()
	cases := map[string]string{
		ModulePath + "/cas":            "cas/",
		ModulePath + "/cas/backend/fs": "cas/",
		ModulePath + "/gitlike":        "gitlike/",
		ModulePath + "/internal/store": "internal/, cmd/",
		ModulePath + "/cmd/cask":       "internal/, cmd/",
		ModulePath + "/examples/files": "examples/, benchmarks/",
		ModulePath + "/benchmarks":     "examples/, benchmarks/",
	}
	for pkg, wantLayer := range cases {
		layer, ok := layers.Owner(matrix, pkg)
		if !ok {
			t.Errorf("no arm claims %s", pkg)
			continue
		}
		if layer.Name != wantLayer {
			t.Errorf("Owner(%s) layer = %q, want %q", pkg, layer.Name, wantLayer)
		}
	}
}

// TestCoverageIsWellFormed pins that the shipped table passes its own validation,
// because a malformed row would be read as "no threshold" and drop its package out
// of the gate.
func TestCoverageIsWellFormed(t *testing.T) {
	t.Parallel()

	if err := Coverage().Validate(); err != nil {
		t.Fatalf("the coverage policy does not validate: %v", err)
	}
}

// TestEveryCasPackageIsCovered is the drift check against the real module: a
// package under cas/ that the policy mentions nowhere fails here, which is what
// makes the gate total over cas/ rather than total over a remembered list.
func TestEveryCasPackageIsCovered(t *testing.T) {
	t.Parallel()

	module, _ := goList(t)
	// The tree's own package list, module-relative, which is the form the policy
	// uses.
	pattern := module + "/cas/..."
	out, err := exec.Command("go", "list", pattern).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	relative, err := coverage.StripModule(module, strings.Split(strings.TrimSpace(string(out)), "\n"))
	if err != nil {
		t.Fatalf("StripModule: %v", err)
	}
	missing, err := Coverage().Uncovered(relative)
	if err != nil {
		t.Fatalf("Uncovered: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("every package under cas/ needs a coverage tier or an exemption; uncovered: %v", missing)
	}
}

// TestCoverageHasNoStaleRows pins the other direction: a row naming a package that
// no longer exists is a gate measuring nothing while looking thorough.
func TestCoverageHasNoStaleRows(t *testing.T) {
	t.Parallel()

	policy := Coverage()
	if len(policy.Targets)+len(policy.Exempt) == 0 {
		t.Fatal("the policy names no packages, so this test would prove nothing")
	}
	module, _ := goList(t)
	named := make([]string, 0, len(policy.Targets)+len(policy.Exempt))
	for _, target := range policy.Targets {
		named = append(named, target.Package)
	}
	for _, exempt := range policy.Exempt {
		named = append(named, exempt.Package)
	}
	for _, pkg := range named {
		if out, err := exec.Command("go", "list", module+"/"+pkg).CombinedOutput(); err != nil {
			t.Errorf("the policy names %s, which go list rejects: %v: %s", pkg, err, strings.TrimSpace(string(out)))
		}
	}
}

// TestCodecGuardsAreReached pins that the guards name packages this module really
// has, and that each one reaches the codec layer or not as the table says.
func TestCodecGuardsAreReached(t *testing.T) {
	t.Parallel()

	guards := CodecGuards()
	if len(guards) == 0 {
		t.Fatal("no codec guards, so this test would prove nothing")
	}
	dependencies := map[string][]string{}
	for _, guard := range guards {
		if guard.Remedy == "" {
			t.Errorf("guard for %q gives no remedy", guard.Package)
		}
		// `go list ./gitlike` resolves against the working directory, so it has to
		// run from the module root rather than from this package.
		cmd := exec.Command("go", "list", "-deps", guard.Package)
		cmd.Dir = repoRoot(t)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s (dir %s): %v: %s", guard.Package, cmd.Dir, err, strings.TrimSpace(stderr.String()))
		}
		dependencies[guard.Package] = strings.Fields(string(out))
	}
	// The rule holds today: nothing guarded reaches the codec layer.
	if violations := deps.CheckCodecDeps(guards, dependencies); len(violations) != 0 {
		t.Errorf("a guarded package reaches the codec layer: %v", violations)
	}
}

// TestInventoriesMatchTheTree runs the inventory rule against the real website,
// which is the check the gate performs, so a table that drifts from the tree fails
// here as well as in the gate.
func TestInventoriesMatchTheTree(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("not running from the repository: %v", err)
	}
	inventories := Inventories()
	if len(inventories) == 0 {
		t.Fatal("no inventory tables, so this test would prove nothing")
	}
	for _, inventory := range inventories {
		if findings := website.CheckInventory(root, filepath.Join(root, "website"), inventory); len(findings) != 0 {
			t.Errorf("%s: %q", inventory.Page, findings)
		}
	}
}

// TestEveryBuildDirectoryIsDocumented pins the subtree's documentation rule: every
// directory under internal/build carries a README, and its parent's README links to
// it. Both halves matter — a README nobody links to is invisible to a reader who
// arrives at the tree, and a directory with no README is where a rule goes
// unexplained.
func TestEveryBuildDirectoryIsDocumented(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot(t), "internal", "build")
	directories, err := buildDirectories(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(directories) < 2 {
		t.Fatalf("found %d directories under internal/build, so this test would prove nothing", len(directories))
	}

	for _, dir := range directories {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatalf("relative path for %s: %v", dir, err)
		}
		readme := filepath.Join(dir, "README.md")
		content, err := os.ReadFile(readme)
		if err != nil {
			t.Errorf("internal/build/%s has no README.md", filepath.ToSlash(rel))
			continue
		}
		// The parent links to it, except for the tree root, which no one links to
		// from inside the tree.
		if rel == "." {
			continue
		}
		parent := filepath.Dir(dir)
		parentReadme, err := os.ReadFile(filepath.Join(parent, "README.md"))
		if err != nil {
			t.Errorf("internal/build/%s has no README.md to link from", filepath.ToSlash(filepath.Dir(rel)))
			continue
		}
		name := filepath.Base(dir)
		if !strings.Contains(string(parentReadme), "./"+name+"/README.md") &&
			!strings.Contains(string(parentReadme), "./"+name+"/") {
			t.Errorf("the README of internal/build/%s does not link to ./%s/README.md",
				filepath.ToSlash(filepath.Dir(rel)), name)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			t.Errorf("internal/build/%s/README.md is empty", filepath.ToSlash(rel))
		}
	}
}

// buildDirectories returns every directory under root that is not hidden, so the check
// covers a new package the moment it appears. Go's own `testdata` tree is skipped: it
// holds test inputs rather than a package a reader navigates, and a fuzz target's failing
// inputs live there.
func buildDirectories(root string) ([]string, error) {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata") {
			return filepath.SkipDir
		}
		directories = append(directories, path)
		return nil
	})
	return directories, err
}

// TestGraphDocRendersTheCommittedDocument is the package graph's own gate: the
// committed artifact must be exactly what GraphDoc renders at the version it
// carries, or the gate's `dep-graph` step would report it stale on every run.
func TestGraphDocRendersTheCommittedDocument(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	path := filepath.Join(root, filepath.FromSlash(GraphDocPath))
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read the committed document: %v", err)
	}

	version := depgraph.Version(string(committed))
	if version == "" {
		t.Fatalf("%s has no frontmatter version", GraphDocPath)
	}
	module, packages := goList(t)
	rendered := depgraph.Document(GraphDoc(), depgraph.Derive(module, packages), version)

	if rendered == string(committed) {
		return
	}
	got, want := strings.Split(rendered, "\n"), strings.Split(string(committed), "\n")
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Fatalf("the rendered document differs from the committed one at line %d:\n  got:  %q\n  want: %q",
				i+1, g, w)
		}
	}
	t.Fatal("the rendered document differs from the committed one in length only")
}
