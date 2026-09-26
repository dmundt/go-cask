---
type: Guide
title: versioning (build engine) — go-cask
description: Version-field rule — a changed versioned file must move its frontmatter version.
version: v3
---

# versioning

Rule: a versioned file that changes must move its frontmatter `version:`.

## What it decides

- **Judged** = the base revision carried a version field.
- Judged → does the working file's version differ?
- Judged + unmoved → `Unbumped` reports it, sorted → two runs log identically.
- Absent from the base revision → **not judged**, skipped: "base had a version → the change
  must move it" → a new document cannot break it.

## Reading the field

`Field` = the `docs` package's frontmatter reader, re-exported → one import for a caller of
this rule. One reader in the engine → the rule and the document renderer cannot disagree.

```go
was := versioning.Field(baseContent)
now := versioning.Field(workingContent)
results = append(results, versioning.Result{
    Path:   path,
    Before: was,
    After:  now,
    Judged: was != "",
})

for _, path := range versioning.Unbumped(results) {
    fmt.Fprintln(os.Stderr, path)
}
```

## Not in scope

- No **materiality** judgment: whether a change deserved a bump = a reviewer's call.
- No Git read: the caller supplies before + after → testable without a repository.

## Name

Named for the **rule**, not the field it reads: reading the field is `docs`'s job. The
decision does not depend on the field being called `version:` or the file being Markdown.
`cmd/buildtool version-fields` reads Git + the working tree for the two values; the names
differ on purpose: one is a rule, the other says what it reads.

## Testing

`go test ./versioning/` — the field extractor against the shapes a document can have (no
frontmatter, an unclosed block, a version after the closing fence, trailing space, CRLF),
and the decision for bumped, unbumped, new and unjudged paths.
