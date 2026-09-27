package policy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/website"
)

// readRepoFile returns a repository-relative file's contents with line endings
// normalised, so a check does not depend on the checkout's autocrlf setting. The
// folded-scalar reader in the engine matches a line ending in `>-`, which a CRLF
// checkout would otherwise hide.
func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// TestSiteFooterContract runs the published footer's rules against the real tree:
// the base line mkdocs.yml declares, the line the hook composes from a revision and
// from none at all, and the text around them that must — or must not — be there.
//
// This is the check the gate's website-footer step performs, so a footer that drifts
// fails here as well as in the gate.
func TestSiteFooterContract(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	footer := SiteFooter()

	document := readRepoFile(t, root, footer.Config)
	base, found := website.FoldedScalar(document, footer.Spec.Key)
	if !found {
		t.Fatalf("%s: no folded `%s: >-` value; the footer line has nowhere to live",
			footer.Config, footer.Spec.Key)
	}
	if base != footer.Expected {
		t.Errorf("%s %s = %q, want %q", footer.Config, footer.Spec.Key, base, footer.Expected)
	}

	// The year follows the `&copy;` token, so every year case is the base line
	// with that one insertion.
	wantYear := strings.Replace(footer.Expected, footer.Spec.YearToken, footer.Spec.YearToken+" 2026", 1)
	cases := []struct {
		name     string
		date     string
		revision string
		want     string
	}{
		{
			name:     "revision and its commit date",
			date:     "2026-09-23",
			revision: "ab7deab",
			want: wantYear + " " + footer.Spec.Separator + footer.Spec.Label +
				` <a href="` + footer.Spec.CommitURL + `ab7deab" title="commit ab7deab">2026-09-23</a>`,
		},
		{
			// A tarball build, no git, no environment variable: the footer
			// degrades to the line mkdocs.yml declares.
			name: "no readable revision",
			want: footer.Expected,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := website.FooterLine(base, tc.date, tc.revision, footer.Spec); got != tc.want {
				t.Errorf("footer line = %q, want %q", got, tc.want)
			}
			if findings := website.FooterFindings(base, tc.date, tc.revision, footer.Spec); len(findings) != 0 {
				t.Errorf("the footer breaks its own contract: %q", findings)
			}
		})
	}
}

// TestSiteFooterMachineryStaysRemoved guards the deletions the one-line footer is
// made of: the vendored partial, the theme override, the CI provenance step, the
// SITE_* plumbing and the CSS for the second line must not come back.
func TestSiteFooterMachineryStaysRemoved(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	footer := SiteFooter()

	contents := map[string]string{}
	for _, guard := range footer.Guards {
		contents[guard.Path] = readRepoFile(t, root, guard.Path)
	}
	findings := website.CheckSourceGuards(contents, footer.Guards)
	findings = append(findings, website.CheckAbsentPaths(footer.RemovedPaths, func(path string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		return err == nil
	})...)
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestSiteFooterSelftestPasses runs the hook's own self-test, which composes the
// footer line for fixed inputs with the shipping configuration and fails on a
// changed rendered text, a zone label, a second line, a config that lost the line,
// or a date that was guessed instead of omitted. It is the authority on the rendered
// text, and the gate's website-footer step runs the same call; where no interpreter
// is usable the module is still checked by the source guards above.
func TestSiteFooterSelftestPasses(t *testing.T) {
	t.Parallel()

	footer := SiteFooter()
	python := usableInterpreter()
	if python == "" {
		t.Skip("no usable python3/python interpreter; the gate's website-footer step runs the self-test")
	}
	cmd := exec.Command(python, footer.Selftest...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("%s failed: %v\n%s", strings.Join(footer.Selftest, " "), err, out)
		}
		t.Skipf("cannot run %s: %v", python, err)
	}
	if !strings.Contains(string(out), footer.SelftestConfirmation) {
		t.Errorf("%s printed no confirmation of the pinned line:\n%s",
			strings.Join(footer.Selftest, " "), out)
	}
}

// usableInterpreter returns the first python3/python on PATH that actually runs. A
// Windows "app execution alias" for python3 exists but only prints an error and
// exits non-zero, so a name on PATH is not enough.
func usableInterpreter() string {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "--version").Run(); err == nil {
			return path
		}
	}
	return ""
}
