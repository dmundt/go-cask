package adler32_test

import (
	"fmt"

	adler32 "github.com/dmundt/go-cask/cas/verify/adler32"
)

func Example() {
	d := adler32.Of([]byte("hello world"))
	fmt.Println(adler32.Format(d))
	// Output:
	// adler32:1a0b045d
}
