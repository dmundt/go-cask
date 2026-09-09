package gob_test

import (
	"fmt"

	"github.com/dmundt/go-cask/cas/codec/gob"
)

// Example shows the gob codec round-trip (cas-core §4.6).
func Example() {
	c := gob.New[obj]()
	data, err := c.Marshal(obj{Title: "hi", Body: "go"})
	if err != nil {
		fmt.Println("marshal error:", err)
		return
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		fmt.Println("unmarshal error:", err)
		return
	}
	fmt.Printf("%s/%s\n", got.Title, got.Body)
	// Output:
	// hi/go
}
