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
	inline := read(`\.viewer-table th,\n\.viewer-table td \{\n  padding: 8px (\d+px);`)
	left := read(`tr > :first-child \{\n  padding-left: (\d+px);`)
	right := read(`tr > :last-child \{\n  padding-right: (\d+px);`)
	mirrors := []string{
		"  margin: -8px -" + inline + ";\n  padding: 8px " + inline + ";",
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
	if !strings.Contains(css, "font: 11px/1.45 var(--viewer-mono);") {
		t.Error("digest field no longer uses the reduced 11px mono font")
	}
}

func TestControlFontResetCannotBeatComponentRules(t *testing.T) {
	// The control font reset normalises the UA font onto the shell font, but it
	// must not decide the size: a plain `.viewer-shell button` selector outranks
	// every single-class component rule, so a control declaring its own size
	// silently kept the 15.4px body font. :where() drops the reset to zero
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
	// declares no font inherits the 15.4px body size, which is set for prose and
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
		".viewer-verify-all",
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
}
