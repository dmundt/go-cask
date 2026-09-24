package crc32_test

import (
	"fmt"

	crc32 "github.com/dmundt/go-cask/cas/verify/crc32"
)

// Example shows the CRC-32 hasher: the same checksum addresses the bytes and
// later verifies them, without changing the storage model.
func Example() {
	d := crc32.Of([]byte("hello world"))
	fmt.Println(crc32.Format(d))
	// Output:
	// crc32:0d4a1185
}
