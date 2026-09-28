// Package website holds the checks that keep a documentation site honest about the
// code it publishes.
//
// Three rules:
//
//   - Every Go fence on a page is a complete unit: it declares its own package
//     clause and imports, so a reader can copy it into a file and it compiles. The
//     fences are materialized into one package per block, which the caller then
//     builds and vets.
//   - A shipped-package inventory table matches the tree it documents, so a new
//     component cannot ship undocumented and a removed one cannot linger in the
//     table.
//   - The published footer is the one line the site's configuration declares plus
//     the revision the site's hook completes it with: the year is derived, the
//     revision stays in the link target, and the machinery a redesign deleted stays
//     deleted.
//
// Extraction and the inventory comparison are separate functions so each is
// testable on its own: writing to a scratch directory, walking the tree and naming
// the tables are the caller's job, which keeps these rules provable without a
// repository. The footer rules follow the same split — the configuration key, the
// line the caller pins and the guards on the surrounding text are all parameters.
package website

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// fenceLine matches a code-fence delimiter line and captures its marker and info
// string, e.g. "```go" -> ("```", "go").
var fenceLine = regexp.MustCompile(`^\s*(` + "```" + `+|~~~+)\s*(.*?)\s*$`)

// Block is one Go fence found on a page.
type Block struct {
	// Page is the page path relative to the website root, with forward slashes.
	Page string
	// Line is the 1-based line the fence opens on.
	Line int
	// Info is the fence's info string as written.
	Info string
	// Body is the fence's contents, one entry per line.
	Body []string
}

// Dir returns the scratch directory name for a block: the page path with its
// extension dropped and separators replaced, plus the block's index on the page.
// The name is stable, so a materialized copy can be traced back to its fence.
func (b Block) Dir(index int) string {
	slug := strings.ReplaceAll(strings.TrimSuffix(b.Page, ".md"), "/", "-")
	return fmt.Sprintf("%s-%d", slug, index)
}

// GoBlocks returns every fenced block on a page in order, whatever its language,
// so the caller can both materialize the Go ones and report a Go fence whose info
// string is not exactly "go". The original Python selected blocks whose info
// string *contained* "go", so a fence tagged `text` was never considered a Go
// block and never an error; this keeps that boundary.
func GoBlocks(page, content string) []Block {
	lines := splitLines(content)
	var blocks []Block
	var (
		marker string
		start  int
		info   string
		body   []string
	)
	for number, line := range lines {
		match := fenceLine.FindStringSubmatch(line)
		if marker == "" {
			if match != nil {
				marker = strings.Repeat(string(match[1][0]), 3)
				start = number + 1
				info = match[2]
				body = nil
			}
			continue
		}
		if match != nil && strings.HasPrefix(match[1], marker) {
			// A closing fence yields the block if it was a Go one. The original
			// yielded every block and let the caller filter, which is why a
			// non-Go fence here is kept for the info-string check.
			blocks = append(blocks, Block{Page: page, Line: start, Info: info, Body: body})
			marker = ""
			continue
		}
		body = append(body, line)
	}
	return blocks
}

// Materialize extracts every Go block under websiteRoot into scratchRoot, one
// directory per block, and returns the pages read plus the blocks written.
//
// A Go fence must use the info string "go" alone, and its first non-blank line
// must be a package clause: a block that is a fragment rather than a unit is
// reported instead of written, because a reader copies these blocks verbatim
// (website/AGENT.md, "Examples and links").
func Materialize(websiteRoot, scratchRoot string) (pages int, written []Block, findings []string) {
	pages = 0
	var pageFiles []string
	err := filepath.WalkDir(websiteRoot, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(p, ".md") {
			pageFiles = append(pageFiles, p)
		}
		return nil
	})
	if err != nil {
		return 0, nil, []string{fmt.Sprintf("walking %s: %v", websiteRoot, err)}
	}
	sort.Strings(pageFiles)

	if err := os.RemoveAll(scratchRoot); err != nil {
		return 0, nil, []string{fmt.Sprintf("clearing %s: %v", scratchRoot, err)}
	}
	if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
		return 0, nil, []string{fmt.Sprintf("creating %s: %v", scratchRoot, err)}
	}

	for _, file := range pageFiles {
		pages++
		relative, err := filepath.Rel(websiteRoot, file)
		if err != nil {
			return pages, written, append(findings, fmt.Sprintf("%s: %v", file, err))
		}
		page := filepath.ToSlash(relative)
		content, err := os.ReadFile(file)
		if err != nil {
			return pages, written, append(findings, fmt.Sprintf("%s: %v", page, err))
		}

		index := 0
		for _, block := range GoBlocks(page, string(content)) {
			// A block is a candidate only when the fence's language is "go": a
			// `text`, `bash` or `mermaid` fence is ordinary documentation and is
			// neither materialized nor an error.
			if firstField(block.Info) != "go" {
				continue
			}
			if block.Info != "go" {
				// The language is right but the info string carries more, so the
				// reader can copy the block verbatim only by accident.
				findings = append(findings, fmt.Sprintf(
					"%s:%d: a Go fence must use the info string \"go\" alone, not %q",
					page, block.Line, block.Info))
				continue
			}
			index++
			first := firstNonBlank(block.Body)
			if !strings.HasPrefix(first, "package ") {
				findings = append(findings, fmt.Sprintf(
					"%s:%d: Go block is not a complete unit; every Go block must declare its own "+
						"package clause and imports (website/AGENT.md, \"Examples and links\")",
					page, block.Line))
				continue
			}
			dir := filepath.Join(scratchRoot, block.Dir(index))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return pages, written, append(findings, fmt.Sprintf("%s: %v", dir, err))
			}
			body := strings.Join(block.Body, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(dir, "example.go"), []byte(body), 0o644); err != nil {
				return pages, written, append(findings, fmt.Sprintf("%s: %v", dir, err))
			}
			written = append(written, block)
		}
	}
	// The page walk order is a filesystem detail, and findings name a page and a
	// line, so the caller prints them in a stable order.
	sort.Strings(findings)
	return pages, written, findings
}

// Inventory is one shipped-package inventory table: a page that documents the
// packages under a tree. A site states its own tables — this package ships none.
type Inventory struct {
	// Page is the page path relative to the website root.
	Page string
	// Root is the tree the table documents, e.g. "cas/backend".
	Root string
}

// ShippedPackages returns the packages that exist under a tree: each immediate
// subdirectory that holds at least one .go file. A directory with no Go source is
// not a shipped package, which is what keeps a docs-only or testdata directory
// out of the table.
func ShippedPackages(repoRoot, root string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(root)))
	if err != nil {
		return nil, err
	}
	shipped := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(root), entry.Name()))
		if err != nil {
			continue
		}
		for _, child := range children {
			if strings.HasSuffix(child.Name(), ".go") {
				shipped[root+"/"+entry.Name()] = true
				break
			}
		}
	}
	return shipped, nil
}

// DocumentedPackages returns the packages an inventory page names in its table
// rows, as backtick-quoted paths under root. Only table rows are read, so a
// package mentioned in prose does not count as documented.
func DocumentedPackages(pageContent, root string) []string {
	token := regexp.MustCompile("`(" + regexp.QuoteMeta(root) + "/[A-Za-z0-9_]+)`")
	seen := map[string]bool{}
	var documented []string
	for _, line := range splitLines(pageContent) {
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			continue
		}
		for _, match := range token.FindAllStringSubmatch(line, -1) {
			if !seen[match[1]] {
				seen[match[1]] = true
				documented = append(documented, match[1])
			}
		}
	}
	sort.Strings(documented)
	return documented
}

// CheckInventory compares one inventory table against the tree and returns the
// findings. A missing row means a shipped package is undocumented; an extra row
// means the table names a package that no longer exists.
func CheckInventory(repoRoot, websiteRoot string, inventory Inventory) []string {
	var findings []string
	pagePath := filepath.Join(websiteRoot, filepath.FromSlash(inventory.Page))
	content, err := os.ReadFile(pagePath)
	if err != nil {
		return []string{fmt.Sprintf("%s: inventory page is missing", inventory.Page)}
	}

	shipped, err := ShippedPackages(repoRoot, inventory.Root)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", inventory.Root, err)}
	}
	documented := map[string]bool{}
	for _, pkg := range DocumentedPackages(string(content), inventory.Root) {
		documented[pkg] = true
	}

	var missing, extra []string
	for pkg := range shipped {
		if !documented[pkg] {
			missing = append(missing, pkg)
		}
	}
	for pkg := range documented {
		if !shipped[pkg] {
			extra = append(extra, pkg)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) != 0 {
		findings = append(findings, fmt.Sprintf("%s: inventory table is missing %s",
			inventory.Page, strings.Join(missing, ", ")))
	}
	if len(extra) != 0 {
		findings = append(findings, fmt.Sprintf("%s: inventory table names packages that do not exist: %s",
			inventory.Page, strings.Join(extra, ", ")))
	}
	return findings
}

// firstField returns the first space-separated field of a fence info string.
func firstField(info string) string {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// firstNonBlank returns the first line that is not blank.
func firstNonBlank(lines []string) string {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// splitLines splits on LF, matching the original's `splitlines()` for the line
// endings this repository uses.
func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Split(content, "\n")
}
