module github.com/dmundt/go-cask

go 1.24.0

toolchain go1.27.1

require golang.org/x/sys v0.41.0

require github.com/dmundt/go-cask/internal/build/core v0.0.0

replace github.com/dmundt/go-cask/internal/build/core => ./internal/build/core
