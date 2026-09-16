package gzip_test

import (
	"fmt"

	"github.com/dmundt/go-cask/cas/codec/gzip"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type item struct {
	Title string
	Body  string
}

func Example() {
	c := gzip.New(jsoncodec.New[item]())
	data, err := c.Encode(item{Title: "hello", Body: "world"})
	if err != nil {
		fmt.Println("marshal error:", err)
		return
	}
	got, err := c.Decode(data)
	if err != nil {
		fmt.Println("unmarshal error:", err)
		return
	}
	fmt.Printf("%s/%s\n", got.Title, got.Body)
	// Output:
	// hello/world
}
