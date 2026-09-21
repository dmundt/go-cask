package main

import (
	"fmt"
	"runtime"

	"github.com/dmundt/go-cask/internal/web"
)

// runVersion prints the library version and the Go version. The library
// version comes from build info (the module version; pseudo-version until
// the first tag — versioning §2). The viewer renders the same string, so both
// read it from one place.
func runVersion() {
	fmt.Printf("cask %s\n", web.Version())
	fmt.Printf("go %s (%s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
