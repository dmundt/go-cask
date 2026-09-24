// Package website pins the contracts of the published documentation site that
// the Go gate can check without running MkDocs.
//
// Issue #224 replaced the footer's second line, its vendored Material partial
// (website/overrides/) and its CI provenance step with one line that
// website/macros.py composes: the hook reads the checked-out revision with a
// single call and writes the result back to `env.conf["copyright"]`, the value
// the theme's own footer partial renders. This package checks the artifacts the
// gate can see:
//
//   - mkdocs.yml's `copyright` value — extracted exactly as MkDocs folds it —
//     and the line website/macros.py composes from it are pinned for a revision
//     and for no revision at all, year and omitted fragment included. A zone
//     label, a second line, or a guessed date fails here.
//   - The machinery #224 deleted stays deleted: no website/overrides/, no
//     theme.custom_dir, no extra.SITE_* keys, no provenance CI step, and no CSS
//     rule for the removed second line.
//   - website/macros.py reads the revision with the documented single git call
//     and keeps the self-test that scripts/verify.sh runs in the gate; that
//     self-test is executed here whenever a Python interpreter is available,
//     because it is the authority on the rendered text.
package website

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	configPath  = "mkdocs.yml"
	macrosPath  = "website/macros.py"
	verifyPath  = "scripts/verify.sh"
	websitePath = ".github/workflows/website.yml"
	stylesPath  = "website/stylesheets/extra.css"
	overrides   = "website/overrides"

	macrosSelftestCall = "python3 website/macros.py --selftest"

	// commitURL is where the footer's one visible provenance value points: the
	// date is shown, the revision is the link target.
	commitURL = "https://github.com/dmundt/go-cask/commit/"
)

var (
	// yearLiteral matches a four-digit year, which the footer must derive from
	// the deployed revision's date instead of carrying as a literal.
	yearLiteral = regexp.MustCompile(`\b(?:19|20)\d\d\b`)
	// tagPattern matches a tag with its attributes, so visibleText can strip
	// the markup that legitimately carries the revision.
	tagPattern = regexp.MustCompile(`<[^>]*>`)
	// removedMachinery lists, per file, the strings #224 deleted. None of them
	// may come back. website/macros.py is absent here because "provenance" is
	// the name of its own function, not of the deleted CI plumbing.
	removedMachinery = map[string][]string{
		configPath:  {"custom_dir", "SITE_BUILD_DATE", "SITE_REVISION"},
		websitePath: {"provenance", "SITE_BUILD_DATE", "SITE_REVISION", "HEAD_COMMIT", "github.sha"},
		stylesPath:  {"md-copyright__site-build"},
		macrosPath:  {"SITE_BUILD_DATE", "SITE_REVISION"},
	}
)

// repoRoot resolves the repository root from this source file, so the checks
// do not depend on the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test source to resolve the repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// repoFile returns the contents of a repository-relative file with line
// endings normalised, so the assertions do not depend on the checkout's
// autocrlf setting.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// configCopyright returns the `copyright` value mkdocs.yml declares, folded the
// way YAML folds a `>-` scalar. It is deliberately strict: the self-test in
// website/macros.py understands exactly this shape, so a configuration that
// stops using it must fail here rather than quietly pin nothing.
func configCopyright(t *testing.T, config string) string {
	t.Helper()
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != "copyright: >-" {
			continue
		}
		var parts []string
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			if !strings.HasPrefix(next, "  ") || strings.HasPrefix(trimmed, "#") {
				break
			}
			parts = append(parts, trimmed)
		}
		return strings.Join(parts, " ")
	}
	t.Fatalf("%s: no folded `copyright: >-` value; the footer line has nowhere to live", configPath)
	return ""
}

// composeCopyright completes the base line the way website/macros.py does: the
// revision's year follows the `&copy;` token and the line closes with that
// revision's date, linked to the commit, and either is omitted — never guessed —
// when its value is missing.
func composeCopyright(base, date, revision string) string {
	line := strings.Join(strings.Fields(base), " ")
	if date == "" {
		return line
	}
	line = strings.Replace(line, "&copy;", "&copy; "+date[:4], 1)
	if revision != "" {
		line += " &middot; <a href=\"" + commitURL + revision +
			"\" title=\"commit " + revision + "\">" + date + "</a>"
	}
	return line
}

// visibleText drops tags and their attributes, so the checks below can assert
// what a reader sees rather than what the markup carries.
func visibleText(line string) string {
	return tagPattern.ReplaceAllString(line, "")
}

// TestFooterCopyrightLinePinsRenderedText pins the exact footer line for a
// build with a revision and for one without, so a zone label, a second line, a
// lost link, or a guessed year fails here instead of on the published site.
func TestFooterCopyrightLinePinsRenderedText(t *testing.T) {
	base := configCopyright(t, repoFile(t, configPath))

	const wantBase = `&copy; Daniel Mundt &middot; <a href="/impressum/">Impressum</a> &middot; <a href="/privacy/">Privacy</a> &middot; <a href="https://github.com/dmundt/go-cask">GitHub</a>`
	if base != wantBase {
		t.Errorf("%s copyright = %q, want %q", configPath, base, wantBase)
	}
	if literal := yearLiteral.FindString(base); literal != "" {
		t.Errorf("%s: the copyright year %s is a literal; it must derive from the deployed revision's date", configPath, literal)
	}

	// The year follows the `&copy;` token, so every year case is the base line
	// with that insertion.
	wantYear := strings.Replace(wantBase, "&copy;", "&copy; 2026", 1)

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
			want: wantYear + " &middot; <a href=\"" + commitURL + "ab7deab\"" +
				" title=\"commit ab7deab\">2026-09-23</a>",
		},
		{
			// A tarball build, no git, no environment variable: the footer
			// degrades to the line mkdocs.yml declares.
			name: "no readable revision",
			want: wantBase,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := composeCopyright(base, tc.date, tc.revision)
			if got != tc.want {
				t.Errorf("footer line = %q, want %q", got, tc.want)
			}
			for _, forbidden := range []string{"UTC", "Site built", "\n", "\r"} {
				if strings.Contains(got, forbidden) {
					t.Errorf("footer line %q carries %q; the footer is one line and names no zone", got, forbidden)
				}
			}
			if tc.revision == "" {
				return
			}
			if !strings.Contains(got, commitURL+tc.revision) {
				t.Errorf("footer line %q does not link to commit %s", got, tc.revision)
			}
			if strings.Contains(visibleText(got), tc.revision) {
				t.Errorf("footer line %q shows the revision as visible text; it belongs in the link target", got)
			}
		})
	}
}

// TestFooterMachineryStaysRemoved guards the deletions #224 is made of: the
// vendored partial, the theme override, the CI provenance step, the SITE_*
// plumbing and the CSS for the second line must not come back.
func TestFooterMachineryStaysRemoved(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot(t), filepath.FromSlash(overrides))); !os.IsNotExist(err) {
		t.Errorf("%s still exists (stat error: %v); the theme override must stay deleted", overrides, err)
	}

	for rel, gone := range removedMachinery {
		source := repoFile(t, rel)
		for _, needle := range gone {
			if strings.Contains(source, needle) {
				t.Errorf("%s still mentions %q; issue #224 removed that machinery", rel, needle)
			}
		}
	}
}

// TestMacrosReadsTheRevisionItself pins the hook's side: one git call answers
// both values, no environment variable or zone conversion is involved, and the
// self-test the gate runs stays wired up.
func TestMacrosReadsTheRevisionItself(t *testing.T) {
	source := repoFile(t, macrosPath)

	if !strings.Contains(source, `_git("log", "-1", "--format=%h %cs")`) {
		t.Errorf("%s: the revision must come from one `git log -1 --format=%%h %%cs` call", macrosPath)
	}
	for _, forbidden := range []string{"astimezone", "%cI", "os.environ.get(\"SITE_"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s still contains %q; the values are read from the revision, never converted or injected", macrosPath, forbidden)
		}
	}
	for _, mode := range []string{"--selftest", "--print-copyright"} {
		if !strings.Contains(source, mode) {
			t.Errorf("%s: the %s mode is missing; the gate runs it and it pins the rendered line", macrosPath, mode)
		}
	}
	if !strings.Contains(repoFile(t, verifyPath), macrosSelftestCall) {
		t.Errorf("%s does not run `%s`, so nothing in the gate evaluates the footer", verifyPath, macrosSelftestCall)
	}
}

// TestMacrosSelftestPasses runs the hook's own self-test: it composes the
// footer line for fixed inputs with the shipping configuration and fails on a
// changed rendered text, a zone label, a second line, or a guessed date. The
// gate's environment has a Python interpreter for the doc-integrity step, so
// there it always runs; where none is usable the module is still checked by the
// source assertions above and by scripts/verify.sh.
func TestMacrosSelftestPasses(t *testing.T) {
	python := usableInterpreter()
	if python == "" {
		t.Skip("no usable python3/python interpreter; scripts/verify.sh runs the self-test in the gate")
	}
	cmd := exec.Command(python, filepath.FromSlash(macrosPath), "--selftest")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("%s --selftest failed: %v\n%s", macrosPath, err, out)
		}
		t.Skipf("cannot run %s: %v", python, err)
	}
	if !strings.Contains(string(out), "renders as pinned") {
		t.Errorf("%s --selftest printed no confirmation:\n%s", macrosPath, out)
	}
}

// usableInterpreter returns the first python3/python on PATH that actually runs.
// A Windows "app execution alias" for python3 exists but only prints an error
// and exits non-zero, so a name on PATH is not enough.
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
