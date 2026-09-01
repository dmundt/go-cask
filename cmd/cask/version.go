package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// runVersion prints the library version and the Go version. The library
// version comes from build info (the module version; pseudo-version until
// the first tag — versioning §2).
func runVersion() {
	lib := "dev"
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			lib = bi.Main.Version
		}
	}
	fmt.Printf("cask %s\n", lib)
	fmt.Printf("go %s (%s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
