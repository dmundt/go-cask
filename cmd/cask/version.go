package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"

	"github.com/dmundt/go-cask/internal/web"
)

// versionFlags registers version's flags: the command takes none, but parsing
// its arguments gives it the same -h/-help handling and unknown-flag rejection
// as every other command (cli.md §4).
func versionFlags() *flag.FlagSet {
	return newFlagSet("version")
}

// runVersionCommand handles the version subcommand: no flags, no operands
// (cli.md §2).
func runVersionCommand(_ context.Context, _ modeFlags, args []string) int {
	flags := versionFlags()
	if err := parseFlags(flags, args); err != nil {
		return reportError(err)
	}
	if flags.NArg() != 0 {
		return reportError(usagef("version takes no arguments"))
	}
	runVersion()
	return 0
}

// runVersion prints the library version and the Go version. The library
// version comes from build info (the module version; pseudo-version until
// the first tag — versioning §2). The viewer renders the same string, so both
// read it from one place.
func runVersion() {
	fmt.Printf("cask %s\n", web.Version())
	fmt.Printf("go %s (%s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
