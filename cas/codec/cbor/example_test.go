package cbor_test

import (
	"fmt"

	"github.com/dmundt/go-cask/cas/codec/cbor"
)

type exampleDoc struct {
	Title string `cbor:"title"`
	Body  string `cbor:"body"`
}

// Example shows the CBOR codec round-trip and the explicit field mapping used
// by the package's minimal embedded metadata format.
func Example() {
	encode := func(v exampleDoc) ([]byte, error) {
		return cbor.NewMap().Encode(map[string]any{"title": v.Title, "body": v.Body})
	}
	decode := func(data []byte) (exampleDoc, error) {
		m, err := cbor.NewMap().Decode(data)
		if err != nil {
			return exampleDoc{}, err
		}
		return exampleDoc{Title: m["title"].(string), Body: m["body"].(string)}, nil
	}

	codec := cbor.NewRaw[exampleDoc](encode, decode)
	data, err := codec.Encode(exampleDoc{Title: "hello", Body: "world"})
	if err != nil {
		fmt.Println("encode error:", err)
		return
	}
	got, err := codec.Decode(data)
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("%s/%s\n", got.Title, got.Body)
	// Output:
	// hello/world
}
