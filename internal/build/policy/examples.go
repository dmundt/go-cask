package policy

import "github.com/dmundt/go-cask/internal/build/core/examples"

// ExamplesDir is the tree the example programs live in, relative to the repository
// root.
const ExamplesDir = "examples"

// Examples is go-cask's example table: every example under `examples/`, the store it
// opens in a run's scratch root, and the arguments that make it complete.
//
// The manual `api` example is the one entry a runner must not execute: it is a
// two-process pair (`examples/api/README.md`), so it carries the commands a reader
// starts instead of the arguments a runner passes.
func Examples() []examples.Example {
	return []examples.Example{
		// The CLIs terminate with the subcommand that does the work; each one keeps
		// its store inside the run's scratch root, so a run leaves the working tree
		// untouched.
		{Name: "artifacts", Store: "artifacts", Args: []string{"stats"}},
		{Name: "bloom"},
		{Name: "files", Store: "files", Args: []string{"stats"}},
		{Name: "notes"},
		{Name: "pack", Args: []string{"roundtrip", "8", "hello world"}},
		{
			Name: "api",
			Manual: []string{
				"go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080",
				"go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md",
			},
		},
	}
}
