package receipt

import (
	"strings"
	"testing"
)

// FuzzParse checks the reader a receipt commit's message goes through: never panics, and
// anything it accepts carries the version line it claims to. A reader that accepted an
// unknown layout would compare fields that are not there, and a verification that compares
// nothing is worse than one that refuses.
func FuzzParse(f *testing.F) {
	f.Add(Render(recordFixture()))
	f.Add(Version + "\n")
	f.Add(Version + "\ncommit a\ntree b\nbase c\ndiff d\nscope docs\ncheck gofmt\n")
	f.Add("")
	f.Add("cask-gate-receipt 2\ncommit a\n")
	f.Add("commit a\n")
	f.Add(Version + "\ncommit " + strings.Repeat("a", 4096) + "\n")
	f.Add(Version + "\ncheck \n")
	f.Add(Version + "\nscope\n")
	// A field value carrying a CR: the seed `testdata/fuzz/FuzzParse/seed-cr-in-value`
	// keeps it, and this line keeps it visible beside the property it broke.
	f.Add(Version + "\nscope \r")
	f.Add("cask-gate-receipt 1")

	f.Fuzz(func(t *testing.T, payload string) {
		record, err := Parse(payload)
		if err != nil {
			if !strings.HasPrefix(payload, Version) {
				return
			}
			// The version line is present but did not parse: the only way that happens is a
			// first line that is not exactly the version, which is a refusal either way.
			first, _, _ := strings.Cut(payload, "\n")
			if first == Version {
				t.Fatalf("Parse refused a payload whose first line is the version: %q", payload)
			}
			return
		}

		// Anything accepted is re-renderable, and re-rendering keeps its evidence: the
		// record a caller compares may not lose a field it read.
		again, err := Parse(Render(record))
		if err != nil {
			t.Fatalf("Parse(Render(record)) refused its own output: %v", err)
		}
		if again.Identity() != record.Identity() {
			t.Fatalf("the identity did not survive a render:\n%s\n%s", record.Identity(), again.Identity())
		}
		for _, check := range record.Checks {
			if err := CheckName(check); err != nil {
				t.Fatalf("Parse accepted a check name the format cannot carry: %v", err)
			}
		}
	})
}
