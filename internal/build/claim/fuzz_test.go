package claim

import (
	"strings"
	"testing"
)

// FuzzIssueOf checks the ref reader the lane's commands share: never panics, and anything
// it accepts is an issue number and nothing else. A value it accepted but could not use
// would be handed to the API as a ref segment, so a trailing slash, a path separator or a
// sign has to be a refusal rather than a lane somebody else's claim can be read as.
func FuzzIssueOf(f *testing.F) {
	f.Add("refs/lane/389", "refs/lane/")
	f.Add("refs/lane/1", "refs/lane/")
	f.Add("refs/lane/", "refs/lane/")
	f.Add("refs/lane/38a", "refs/lane/")
	f.Add("refs/lane/38/9", "refs/lane/")
	f.Add("refs/lane/-1", "refs/lane/")
	f.Add("refs/lane/0", "refs/lane/")
	f.Add("refs/heads/main", "refs/lane/")
	f.Add("", "refs/lane/")
	f.Add("refs/lane/389", "")

	f.Fuzz(func(t *testing.T, ref, prefix string) {
		issue, found := IssueOf(ref, prefix)
		if !found {
			if issue != "" {
				t.Fatalf("IssueOf(%q, %q) refused with %q, want the empty string", ref, prefix, issue)
			}
			return
		}
		if issue == "" {
			t.Fatalf("IssueOf(%q, %q) accepted an empty issue", ref, prefix)
		}
		if strings.ContainsAny(issue, "/\\") {
			t.Fatalf("IssueOf(%q, %q) = %q, which carries a path separator", ref, prefix, issue)
		}
		for _, digit := range issue {
			if digit < '0' || digit > '9' {
				t.Fatalf("IssueOf(%q, %q) = %q, which is not all digits", ref, prefix, issue)
			}
		}
		// The issue is the ref's own last segment, so a ref rebuilt from it is the ref the
		// reader was given — the reader trims surrounding space, so the comparison is
		// against the trimmed form.
		if strings.TrimSpace(ref) != prefix+issue {
			t.Fatalf("IssueOf(%q, %q) = %q, which is not the ref's last segment", ref, prefix, issue)
		}
	})
}
