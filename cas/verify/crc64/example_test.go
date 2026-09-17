package crc64_test

import (
	"fmt"

	crc64 "github.com/dmundt/go-cask/cas/verify/crc64"
)

func Example() {
	d := crc64.Of([]byte("hello world"))
	fmt.Println(crc64.Format(d))
	// Output:
	// crc64:53037ecdef2352da
}
