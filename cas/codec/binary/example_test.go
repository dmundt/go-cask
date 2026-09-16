package binary_test

import (
	"bytes"
	"fmt"

	"github.com/dmundt/go-cask/cas/codec/binary"
)

type exampleItem struct {
	Name    string
	Count   int
	Enabled bool
	Data    []byte
}

// Example shows the raw binary codec round-trip: the caller supplies the
// encode/decode functions, and the codec preserves the value on decode.
func Example() {
	codec := binary.NewRaw(
		func(v exampleItem) ([]byte, error) {
			var buf bytes.Buffer
			buf.WriteByte(1)
			buf.WriteByte(byte(len(v.Name)))
			buf.WriteString(v.Name)
			buf.WriteByte(byte(v.Count))
			if v.Enabled {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}
			buf.WriteByte(byte(len(v.Data)))
			buf.Write(v.Data)
			return buf.Bytes(), nil
		},
		func(data []byte) (exampleItem, error) {
			buf := bytes.NewBuffer(data)
			version, err := buf.ReadByte()
			if err != nil || version != 1 {
				return exampleItem{}, fmt.Errorf("bad version")
			}
			nameLen, err := buf.ReadByte()
			if err != nil {
				return exampleItem{}, err
			}
			name := make([]byte, nameLen)
			if _, err := buf.Read(name); err != nil {
				return exampleItem{}, err
			}
			count, err := buf.ReadByte()
			if err != nil {
				return exampleItem{}, err
			}
			enabledByte, err := buf.ReadByte()
			if err != nil {
				return exampleItem{}, err
			}
			payloadLen, err := buf.ReadByte()
			if err != nil {
				return exampleItem{}, err
			}
			payload := make([]byte, payloadLen)
			if _, err := buf.Read(payload); err != nil {
				return exampleItem{}, err
			}
			return exampleItem{
				Name:    string(name),
				Count:   int(count),
				Enabled: enabledByte == 1,
				Data:    payload,
			}, nil
		},
	)

	data, err := codec.Encode(exampleItem{Name: "demo", Count: 7, Enabled: true, Data: []byte{0x1, 0x2, 0x3}})
	if err != nil {
		fmt.Println("encode error:", err)
		return
	}
	got, err := codec.Decode(data)
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("%s/%d/%v\n", got.Name, got.Count, got.Enabled)
	// Output:
	// demo/7/true
}
