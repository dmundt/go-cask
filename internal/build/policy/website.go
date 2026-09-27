package policy

import "github.com/dmundt/go-cask/internal/build/core/website"

// The paths the published footer is made of. They are go-cask's, which is why they
// live here rather than in the engine: the engine checks a footer, it does not know
// which site declares one.
const (
	// FooterConfigPath declares the base line the theme renders verbatim.
	FooterConfigPath = "mkdocs.yml"
	// FooterMacrosPath is the hook that completes the line from the checked-out
	// revision before any page renders.
	FooterMacrosPath = "website/macros.py"
	// FooterVerifyPath is the gate's step list, which has to keep evaluating the footer.
	// It moved out of scripts/verify.sh into the command when the step list did; the
	// entry point that starts the command is pinned by the gate table's own test.
	FooterVerifyPath = "cmd/buildtool/verify.go"
	// FooterWorkflowPath builds and publishes the site.
	FooterWorkflowPath = ".github/workflows/website.yml"
	// FooterStylesPath is the site's one stylesheet.
	FooterStylesPath = "website/stylesheets/extra.css"
	// FooterOverridesPath was the vendored theme partial the one-line footer
	// replaced; a directory here means the override came back.
	FooterOverridesPath = "website/overrides"
)

// Footer is go-cask's published-footer contract: the configuration that declares the
// base line, the exact line it must declare, the hook that composes the rendered
// line, and the text that must — or must not — surround them.
//
// Issue #224 collapsed the footer from two lines, a vendored Material partial and a
// CI provenance step into the one line the hook composes. A zone label, a second
// line, a guessed date or a returned override are all regressions, so each is pinned
// here as data a reader can change in one place.
type Footer struct {
	// Spec is what the engine's footer rules read.
	Spec website.FooterSpec
	// Config is the configuration that declares the base line, and Expected is the
	// line it must declare — the footer as it reads when no revision can be read.
	Config   string
	Expected string
	// Macros is the hook that composes the rendered line, Selftest the argv that
	// composes it for fixed inputs with the shipping configuration, and
	// SelftestConfirmation the phrase it prints when the rendered text is the
	// pinned one — so a passing exit status alone is not the check.
	Macros               string
	Selftest             []string
	SelftestConfirmation string
	// RemovedPaths the redesign deleted: a path here that exists again is the
	// regression, not a missing feature.
	RemovedPaths []string
	// Guards pin the text around the footer, per file: what must still be there,
	// and the machinery that must not come back.
	Guards []website.SourceGuard
}

// SiteFooter returns go-cask's footer contract. It is a function rather than a
// package-level variable so a caller cannot mutate the gate's policy by accident.
func SiteFooter() Footer {
	return Footer{
		Spec: website.FooterSpec{
			Key:       "copyright",
			YearToken: "&copy;",
			Separator: "&middot; ",
			Label:     "Updated",
			CommitURL: "https://github.com/dmundt/go-cask/commit/",
			// A build zone and a second provenance phrase are what the redesign
			// removed: the line names the commit's own date and nothing else.
			Forbidden: []string{"UTC", "Site built"},
		},
		Config: FooterConfigPath,
		Expected: `&copy; Daniel Mundt &middot; <a href="/impressum/">Impressum</a>` +
			` &middot; <a href="/privacy/">Privacy</a>` +
			` &middot; <a href="https://github.com/dmundt/go-cask">GitHub</a>`,
		Macros:               FooterMacrosPath,
		Selftest:             []string{"website/macros.py", "--selftest"},
		SelftestConfirmation: "renders as pinned",
		RemovedPaths:         []string{FooterOverridesPath},
		Guards: []website.SourceGuard{
			{
				// The theme override, the injected provenance values and the second
				// line's CSS must not come back.
				Path:    FooterConfigPath,
				MustNot: []string{"custom_dir", "SITE_BUILD_DATE", "SITE_REVISION"},
			},
			{
				Path: FooterWorkflowPath,
				MustNot: []string{
					"provenance", "SITE_BUILD_DATE", "SITE_REVISION", "HEAD_COMMIT", "github.sha",
				},
			},
			{
				Path:    FooterStylesPath,
				MustNot: []string{"md-copyright__site-build"},
			},
			{
				// One git call answers both values, no zone conversion and no
				// environment injection is involved, and the self-test the gate runs
				// stays wired up.
				Path: FooterMacrosPath,
				Must: []string{
					`_git("log", "-1", "--format=%h %cs")`,
					"--selftest",
					"--print-copyright",
				},
				MustNot: []string{
					"SITE_BUILD_DATE", "SITE_REVISION", "astimezone", "%cI", `os.environ.get("SITE_`,
				},
			},
			{
				// The self-test is the authority on the rendered text, so the gate must
				// keep asking for it: the step list runs the command that owns the call,
				// and dropping the step is what this guard catches. It follows the step,
				// which moved from scripts/verify.sh to the command's step list.
				Path: FooterVerifyPath,
				Must: []string{`Name: "website footer"`, "runWebsiteFooter("},
			},
		},
	}
}
