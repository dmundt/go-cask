package crc32_test

import (
	"fmt"

	crc32 "github.com/dmundt/go-cask/cas/verify/crc32"
)

// Example shows the maintenance-layer CRC32 helper: it produces a checksum that
// can validate bytes without changing the storage model or the object-address
// layer.
func Example() {
	d := crc32.Of([]byte("hello world"))
	fmt.Println(crc32.Format(d))
	// Output:
	// crc32:0d4a1185
}
