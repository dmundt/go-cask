package examples

import (
	"strings"
	"testing"
)

// table is an example table shaped like a real site's: two automated examples, one of
// them with a store and a subcommand, and one manual pair.
func table() []Example {
	return []Example{
		{Name: "artifacts", Store: "artifacts", Args: []string{"stats"}},
		{Name: "pack", Args: []string{"roundtrip", "8", "hello world"}},
		{Name: "api", Manual: []string{"go run ./examples/api/server", "go run ./examples/api/demo"}},
	}
}

func TestAutomatedAndManualSplit(t *testing.T) {
	t.Parallel()

	automated := Automated(table())
	if len(automated) != 2 || automated[0].Name != "artifacts" || automated[1].Name != "pack" {
		t.Errorf("Automated = %+v, want the two examples that terminate", automated)
	}
	manual := Manual(table())
	if len(manual) != 1 || manual[0].Name != "api" {
		t.Errorf("Manual = %+v, want the pair a reader starts", manual)
	}
}

func TestSelect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		names   []string
		want    []string
		wantErr string
	}{
		{
			name: "no name runs every automated example in table order",
			want: []string{"artifacts", "pack"},
		},
		{
			name:  "a named example is run alone",
			names: []string{"pack"},
			want:  []string{"pack"},
		},
		{
			// The caller's order is kept, so a run reads the way it was asked for.
			name:  "several names keep the order given",
			names: []string{"pack", "artifacts"},
			want:  []string{"pack", "artifacts"},
		},
		{
			name:    "an unknown name is an error",
			names:   []string{"nope"},
			wantErr: "unknown example: nope",
		},
		{
			name:    "a manual example is not run",
			names:   []string{"api"},
			wantErr: "does not terminate on its own",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			selected, err := Select(tc.names, table())
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Select(%q) succeeded, want an error", tc.names)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Select(%q) error = %q, want it to mention %q", tc.names, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select(%q): %v", tc.names, err)
			}
			var names []string
			for _, example := range selected {
				names = append(names, example.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Select(%q) = %q, want %q", tc.names, names, tc.want)
			}
		})
	}
}

// TestManualErrorCarriesTheCommands pins that the refusal is actionable: the error names
// every command the reader has to start, because a runner cannot start them.
func TestManualErrorCarriesTheCommands(t *testing.T) {
	t.Parallel()

	_, err := Select([]string{"api"}, table())
	if err == nil {
		t.Fatal("Select accepted a manual example")
	}
	for _, command := range table()[2].Manual {
		if !strings.Contains(err.Error(), command) {
			t.Errorf("the error %q does not carry %q", err, command)
		}
	}
}

func TestFindFirstMatchWins(t *testing.T) {
	t.Parallel()

	duplicated := []Example{{Name: "a", Args: []string{"first"}}, {Name: "a", Args: []string{"second"}}}
	found, ok := Find(duplicated, "a")
	if !ok || found.Args[0] != "first" {
		t.Errorf("Find = %+v, %v; want the first entry with that name", found, ok)
	}
	if _, ok := Find(duplicated, "b"); ok {
		t.Error("Find reported an example the table does not carry")
	}
}
