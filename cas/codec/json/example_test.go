package json_test

import (
	"fmt"

	"github.com/dmundt/go-cask/cas/codec/json"
)

// Example shows the JSON codec round-trip (cas-core §4.6): New[T]() returns a
// codec and Unmarshal(Marshal(v)) recovers the value.
func Example() {
	c := json.New[obj]()
	data, err := c.Marshal(obj{Title: "hello", Body: "world"})
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
	// hello/world
}
