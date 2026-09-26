package deps

import (
	"strings"
	"testing"
)

// testGuards is a small table in the shape a caller builds. This package ships no
// guards of its own, so the mechanism is exercised against these.
func testGuards() []CodecGuard {
	return []CodecGuard{
		{Package: "./alpha", Remedy: "inject the codec"},
		{Package: "./beta", Remedy: "pass the codec in"},
	}
}

// TestCheckCodecDeps pins the mechanism in both directions and at the path
// boundary: a guarded package that reaches the forbidden prefix fails, one that
// reaches a package merely sharing the prefix does not, and only one violation is
// reported per guarded package however many of its dependencies match.
func TestCheckCodecDeps(t *testing.T) {
	t.Parallel()

	guards := testGuards()
	tests := []struct {
		name string
		deps map[string][]string
		want []string
	}{
		{
			name: "a clean graph has no violations",
			deps: map[string][]string{
				"./alpha": {"core", "core/repo"},
				"./beta":  {"core", "context"},
			},
		},
		{
			name: "a guarded package reaching the layer is a violation",
			deps: map[string][]string{"./alpha": {"core", "cas/codec/json"}},
			want: []string{"./alpha"},
		},
		{
			name: "the forbidden package itself is a violation",
			deps: map[string][]string{"./beta": {"cas/codec"}},
			want: []string{"./beta"},
		},
		{
			name: "a package sharing the prefix is not the layer",
			deps: map[string][]string{"./alpha": {"cas/codecbase"}},
		},
		{
			name: "both guards can fail at once, and are ordered by package",
			deps: map[string][]string{
				"./beta":  {"cas/codec/json"},
				"./alpha": {"cas/codec/gob"},
			},
			want: []string{"./alpha", "./beta"},
		},
		{
			name: "an unguarded package is not checked",
			deps: map[string][]string{"./gamma": {"cas/codec"}},
		},
		{
			name: "an absent package is not a violation",
			deps: map[string][]string{},
		},
		{
			name: "one guarded package reports once, not once per matching dependency",
			deps: map[string][]string{"./alpha": {"cas/codec", "cas/codec/json", "cas/codec/gob"}},
			want: []string{"./alpha"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := CheckCodecDeps(guards, test.deps)
			if len(got) != len(test.want) {
				t.Fatalf("CheckCodecDeps = %v, want %d violation(s)", got, len(test.want))
			}
			for i := range got {
				if got[i].Package != test.want[i] {
					t.Fatalf("violation %d = %q, want %q (all: %v)", i, got[i].Package, test.want[i], got)
				}
			}
		})
	}
}

// TestCodecViolationExplainsItself pins that the message names the rule, the
// offending dependency and the remedy: a gate failure a reader cannot act on is
// barely better than no check.
func TestCodecViolationExplainsItself(t *testing.T) {
	t.Parallel()

	got := CheckCodecDeps(testGuards(), map[string][]string{"./alpha": {"cas/codec/json"}})
	if len(got) != 1 {
		t.Fatalf("CheckCodecDeps = %v, want one violation", got)
	}
	message := got[0].String()
	for _, want := range []string{"./alpha", "cas/codec/json", "inject the codec"} {
		if !strings.Contains(message, want) {
			t.Errorf("violation %q does not mention %q", message, want)
		}
	}
}

// TestIsForbidden pins the path boundary the toolkit owns: a prefix matches only
// on a boundary, so a sibling whose name merely starts with it is not the layer.
func TestIsForbidden(t *testing.T) {
	t.Parallel()

	yes := []string{"cas/codec", "cas/codec/json", "cas/codec/internal/bounded"}
	no := []string{"cas", "cas/codecbase", "cas/encoding", "", "casx/codec"}
	for _, path := range yes {
		if !IsForbidden(path) {
			t.Errorf("IsForbidden(%q) = false, want true", path)
		}
	}
	for _, path := range no {
		if IsForbidden(path) {
			t.Errorf("IsForbidden(%q) = true, want false", path)
		}
	}
}

// TestCheckModuleGraph pins the cases: a real graph, an empty one, one naming a
// different module, and one in which the module is a dependency rather than the
// main module.
func TestCheckModuleGraph(t *testing.T) {
	t.Parallel()

	const module = "example.com/mod"

	// The shape `go list -m -json all` emits: indented, `": "` after the key, and
	// the main module flagged with Main.
	good := `{
	"Path": "` + module + `",
	"Main": true,
	"Dir": "/repo",
	"GoVersion": "1.24.0"
}
{
	"Path": "golang.org/x/sys",
	"Version": "v0.1.0",
	"Dir": "/mod/sys"
}
`
	if err := CheckModuleGraph(module, good); err != nil {
		t.Errorf("CheckModuleGraph rejected a real graph: %v", err)
	}

	for _, test := range []struct {
		name  string
		graph string
	}{
		{name: "empty", graph: ""},
		{name: "whitespace only", graph: "  \n\t\n"},
		{name: "another module entirely", graph: "{\n\t\"Path\": \"example.com/other\",\n\t\"Main\": true\n}\n"},
		{
			// The module appears, but as a requirement: the build was for
			// something else, so a gate would be checking the wrong tree.
			name:  "this module as a dependency only",
			graph: "{\n\t\"Path\": \"example.com/other\",\n\t\"Main\": true,\n\t\"Require\": [\n\t\t{\n\t\t\t\"Path\": \"" + module + "\",\n\t\t\t\"Version\": \"v1.0.0\"\n\t\t}\n\t]\n}\n",
		},
		{
			name:  "the path without the main flag",
			graph: "{\n\t\"Path\": \"" + module + "\"\n}\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := CheckModuleGraph(module, test.graph); err == nil {
				t.Errorf("CheckModuleGraph accepted a graph that does not name the main module:\n%s", test.graph)
			}
		})
	}
}
