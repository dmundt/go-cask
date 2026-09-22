// Tests that pin the rendered geometry and type scale. A stylesheet reads as
// correct far more easily than it behaves, so these assert the computed
// relationships rather than the declarations that produce them.

package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestObjectBrowserDividerMarkup(t *testing.T) {
	content, err := templateFS.ReadFile("templates/objects.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, unwanted := range []string{"data-viewer-divider", `role="separator"`, "data-viewer-workspace"} {
		if strings.Contains(source, unwanted) {
			t.Errorf("objects template still carries the scripted divider markup %q", unwanted)
		}
	}
	// The inspector resizes natively, so the bounds live in CSS rather than in
	// a script that has to enforce them.
	for _, want := range []string{"resize: horizontal", "min-width: 280px", "max-width: 560px"} {
		if !strings.Contains(string(viewerCSS), want) {
			t.Errorf("viewer stylesheet does not contain %q", want)
		}
	}
}

func TestTopBarShowsBuildVersion(t *testing.T) {
	// The viewer identifies the running binary, so an operator reading a bug
	// report can tell which build produced the page. It is the same string the
	// CLI prints: both call Version.
	if Version() == "" {
		t.Fatal("Version must never be empty")
	}
	ts, _ := newTestServer(t)
	viewer := login(t, ts, testStartupToken)
	body := getBody(t, viewer, ts.URL+"/viewer/objects")
	want := `<span class="viewer-version">` + Version() + `</span>`
	if !strings.Contains(body, want) {
		t.Errorf("top bar does not render the build version %q", want)
	}
}

func TestRowLinkInsetMatchesCellPadding(t *testing.T) {
	// The row link fills its cell through a negative margin. When its inset is
	// wider than the cell padding it overhangs the outer columns, and the table
	// grows a horizontal scrollbar for a few phantom pixels. The values are a
	// taste choice, so read them from the cell rules and require only that the
	// link mirrors whatever they say.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	read := func(pattern string) string {
		m := regexp.MustCompile(pattern).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("cell padding rule not found: %s", pattern)
		}
		return m[1]
	}
	inline := read(`\.viewer-table th,\n\.viewer-table td \{\n  height: 26px;\n  padding: 2px (\d+px);`)
	left := read(`tr > :first-child \{\n  padding-left: (\d+px);`)
	right := read(`tr > :last-child \{\n  padding-right: (\d+px);`)
	mirrors := []string{
		"  margin: -2px -" + inline + ";\n  padding: 2px " + inline + ";",
		"  margin-left: -" + left + ";\n  padding-left: " + left + ";",
		"  margin-right: -" + right + ";\n  padding-right: " + right + ";",
	}
	for _, mirror := range mirrors {
		if !strings.Contains(css, mirror) {
			t.Errorf("row link inset no longer mirrors the cell padding: %q", mirror)
		}
	}
}

func TestInspectorDigestFontFitsFullAddress(t *testing.T) {
	// The readonly digest field replaces the copy button, so the whole 64-char
	// address has to fit the inspector.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	if !strings.Contains(css, ".viewer-digest {\n  flex: 1;") {
		t.Error("digest rule is missing")
	}
	if !strings.Contains(css, "font: var(--viewer-label)/1.45 var(--viewer-mono);") {
		t.Error("digest field no longer uses the compact mono label font")
	}
}

func TestInspectorHasListSeparator(t *testing.T) {
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	want := ".viewer-inspector {\n  overflow: auto;"
	start := strings.Index(css, want)
	if start < 0 {
		t.Fatal("inspector rule not found")
	}
	end := strings.Index(css[start:], "}")
	if end < 0 {
		t.Fatal("inspector rule is not closed")
	}
	rule := css[start : start+end]
	if !strings.Contains(rule, "border-left: 1px solid var(--viewer-border);") {
		t.Error("inspector is missing the table separator border")
	}
}

func TestControlFontResetCannotBeatComponentRules(t *testing.T) {
	// The control font reset normalises the UA font onto the shell font, but it
	// must not decide the size: a plain `.viewer-shell button` selector outranks
	// every single-class component rule, so a control declaring its own size
	// silently kept the shell body font. :where() drops the reset to zero
	// specificity, which is what lets the type scale below apply at all.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	if strings.Contains(css, ".viewer-shell button,\n.viewer-shell input,") {
		t.Error("control font reset is specificity-bearing again and will override component font sizes")
	}
	if !strings.Contains(css, ":where(.viewer-shell) button,\n:where(.viewer-shell) input,") {
		t.Error("control font reset is no longer wrapped in :where()")
	}
	// Same trap, different shorthand: `.viewer-meta { margin: 0 }` is declared
	// after the result panel's rules and ties them on specificity, so the
	// panel's top inset only survives with the extra qualifier.
	if !strings.Contains(css, ".viewer-result .viewer-result-meta {\n  margin-top:") {
		t.Error("result meta inset no longer out-specifies the .viewer-meta margin reset")
	}
	// The two secondary notes in the inspector — the empty-references line and
	// the hexdump truncation line — read as the same aside, so they share one
	// rule rather than drifting apart.
	if !strings.Contains(css, ".viewer-reference-empty,\n.viewer-hexdump-note {") {
		t.Error("inspector notes no longer share a single font rule")
	}
}

func TestInteractiveControlsUseTheTypeScale(t *testing.T) {
	// Every control is sized from one of the three scale steps. A control that
	// declares no font inherits the shell body size, which is set for prose and
	// dwarfs a 28px control — that is the bug this guards.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	for _, token := range []string{"--viewer-control:", "--viewer-control-sm:", "--viewer-control-xs:"} {
		if !strings.Contains(css, token) {
			t.Errorf("control type scale is missing %s", token)
		}

	}
	// Each rule below styles an interactive control, so each must name a step.
	controls := []string{
		".viewer-filter-bar input,\n.viewer-filter-bar select,\n.viewer-filter-bar button,\n.viewer-action,\n.viewer-pager a",
		".viewer-verify-all",
		".viewer-reset",
		".viewer-sort-button",
		".viewer-pager a,\n.viewer-pager select,\n.viewer-page-button",
		".viewer-pager label",
		".viewer-inspector-tabs",
		".viewer-history-step",
	}

	scale := regexp.MustCompile(`font(?:-size)?: [^;]*var\(--viewer-control(?:-sm|-xs)?\)`)
	for _, selector := range controls {
		start := strings.Index(css, selector+" {")
		if start < 0 {
			t.Errorf("control rule not found: %q", selector)
			continue
		}
		end := strings.Index(css[start:], "}")
		if end < 0 || !scale.MatchString(css[start:start+end]) {
			t.Errorf("control %q does not size itself from the type scale", selector)
		}
	}

	// One height across the viewer: a control that stands taller than the row
	// it sits in reads as a different kind of control than it is.
	heights := []string{
		".viewer-filter-bar input,\n.viewer-filter-bar select,\n.viewer-filter-bar button,\n.viewer-action,\n.viewer-pager a",
		".viewer-reset",
		".viewer-pager a,\n.viewer-pager select,\n.viewer-page-button",
	}
	for _, selector := range heights {
		start := strings.Index(css, selector+" {")
		if start < 0 {
			t.Errorf("control rule not found: %q", selector)
			continue
		}
		end := strings.Index(css[start:], "}")
		if end < 0 || !strings.Contains(css[start:start+end], "height: 28px;") {
			t.Errorf("control %q does not use the shared 28px height", selector)
		}
	}
	start := strings.Index(css, ".viewer-verify-all {")
	if start < 0 {
		t.Fatal("verify control rule not found")
	}
	end := strings.Index(css[start:], "}")
	if end < 0 || !strings.Contains(css[start:start+end], "height: 26px;") {
		t.Error("verify control does not use the compact 26px button height")
	}
}

func TestWorkbenchVisualTokens(t *testing.T) {
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	for _, token := range []string{
		"--viewer-bg: #f8f8f8;",
		"--viewer-surface: #ffffff;",
		"--viewer-header: #f3f3f3;",
		"--viewer-border: #e5e5e5;",
		"--viewer-fg: #202020;",
		"--viewer-muted: #666666;",
	} {
		if !strings.Contains(css, token) {
			t.Errorf("workbench token missing: %s", token)
		}
	}
	if !strings.Contains(css, "font: var(--viewer-ui)/1.4 var(--viewer-font);") {
		t.Error("viewer shell does not use compact workbench typography")
	}
	for _, legacy := range []string{"9.9px", "12.1px", "12.65px", "13.2px", "19.8px"} {
		if strings.Contains(css, legacy) {
			t.Errorf("viewer stylesheet retains fragmented font size %s", legacy)
		}
	}
	if !strings.Contains(css, "height: 26px;\n  padding: 2px 8px;") {
		t.Error("object rows are not using the 26px workbench density")
	}
	if !strings.Contains(css, "--viewer-accent: #007acc;") {
		t.Error("workbench accent must be VS Code blue")
	}
}

func TestWorkbenchControlsAvoidPillGeometry(t *testing.T) {
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	for _, radius := range []string{"border-radius: 3px;", "border-radius: 4px;", "border-radius: 5px;"} {
		if strings.Contains(css, radius) {
			t.Errorf("workbench stylesheet still contains oversized radius %q", radius)
		}
	}

	if !strings.Contains(css, "border-radius: 0;") || !strings.Contains(css, "border-radius: 2px;") {
		t.Error("workbench stylesheet must use square panels and compact control radii")
	}
	for _, forbidden := range []string{"box-shadow:", "linear-gradient(", "drop-shadow("} {
		if strings.Contains(css, forbidden) {
			t.Errorf("flat workbench stylesheet contains forbidden depth effect %q", forbidden)
		}
	}
}

func TestWorkbenchInteractionStates(t *testing.T) {
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	for _, want := range []string{
		"transition:\n    background-color 120ms ease-out,\n    border-color 120ms ease-out,\n    color 120ms ease-out,\n    filter 120ms ease-out;",
		".viewer-verify-all:active:not(:disabled)",
		".viewer-table tbody tr:focus-within td",
		"background: #cfe1ff;",
		".viewer-status:hover",
		".viewer-status-detached",
		"background: #e9e5f6;",
		"color: #442f70;",
		".viewer-status-root",
		"background: #dce6f7;",
		"color: #1d4f8a;",
		"filter: brightness(0.97);",
		"pointer-events: none;",
		"scrollbar-color: var(--viewer-control-border) transparent;",
		"background: var(--viewer-muted);",
		"scrollbar-width: none;",
		"border: 1px solid rgba(255, 255, 255, 0.65);",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("interaction-state rule missing: %s", want)
		}
	}
}
