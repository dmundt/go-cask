package versioning

import (
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/docs"
)

// FuzzField checks the frontmatter reader every changed file is judged by: it never
// panics on arbitrary bytes, it is deterministic — the gate reads a file once from disk
// and once from a revision — and a document the toolkit's own writer produced reads back
// as the version it was given.
//
// The value is deliberately read verbatim, trailing spaces included (docs.Field): the
// rule compares the field as text, so a reader that normalised it would call `v1` and
// `v1 ` the same version. The writer separates the field from its value with spaces, so a
// value that itself starts or ends with whitespace cannot be represented on one line and
// is out of the round trip's scope.
func FuzzField(f *testing.F) {
	f.Add("v3")
	f.Add("")
	f.Add("latest")
	f.Add("v10")
	f.Add("v1")
	f.Add("v 1")
	f.Add("version: nested")

	f.Fuzz(func(t *testing.T, value string) {
		if again := Field(value); again != Field(value) {
			t.Fatalf("Field(%q) is not deterministic", value)
		}
		if strings.ContainsAny(value, "\r\n") || strings.TrimSpace(value) != value {
			return
		}
		document := docs.Frontmatter([2]string{"title", "x"}, [2]string{"version", value})
		if got := Field(document); got != value {
			t.Fatalf("Field of a document written with version %q = %q", value, got)
		}
	})
}
