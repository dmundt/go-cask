package binary_test

import (
	"bytes"
	stdbinary "encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/dmundt/go-cask/cas/codec/binary"
)

type fuzzItem struct {
	Name    string
	Count   int
	Enabled bool
	Data    []byte
}

func FuzzCodecRoundTrip(f *testing.F) {
	f.Add("demo", 7, true, "payload")
	f.Add("", 0, false, "")
	f.Add("nested", -3, true, "\x00\x01\x02")
	f.Add("weird", 42, false, "hello world")

	f.Fuzz(func(t *testing.T, name string, count int, enabled bool, payload string) {
		codec := binary.NewRaw(
			func(v fuzzItem) ([]byte, error) {
				nameLen := len(v.Name)
				payloadLen := len(v.Data)
				if nameLen > math.MaxInt32 || payloadLen > math.MaxInt32 || v.Count > math.MaxInt32 || v.Count < math.MinInt32 {
					return nil, nil
				}
				var buf bytes.Buffer
				if err := stdbinary.Write(&buf, stdbinary.BigEndian, int32(nameLen)); err != nil {
					return nil, err
				}
				if _, err := buf.WriteString(v.Name); err != nil {
					return nil, err
				}
				if err := stdbinary.Write(&buf, stdbinary.BigEndian, int32(v.Count)); err != nil {
					return nil, err
				}
				if v.Enabled {
					buf.WriteByte(1)
				} else {
					buf.WriteByte(0)
				}
				if err := stdbinary.Write(&buf, stdbinary.BigEndian, int32(payloadLen)); err != nil {
					return nil, err
				}
				if _, err := buf.Write(v.Data); err != nil {
					return nil, err
				}
				return buf.Bytes(), nil
			},
			func(data []byte) (fuzzItem, error) {
				buf := bytes.NewReader(data)
				var nameLen int32
				if err := stdbinary.Read(buf, stdbinary.BigEndian, &nameLen); err != nil {
					return fuzzItem{}, err
				}
				var nameBytes []byte
				if nameLen > 0 {
					nameBytes = make([]byte, int(nameLen))
					if _, err := buf.Read(nameBytes); err != nil {
						return fuzzItem{}, err
					}
				}
				var count int32
				if err := stdbinary.Read(buf, stdbinary.BigEndian, &count); err != nil {
					return fuzzItem{}, err
				}
				enabledByte, err := buf.ReadByte()
				if err != nil {
					return fuzzItem{}, err
				}
				var payloadLen int32
				if err := stdbinary.Read(buf, stdbinary.BigEndian, &payloadLen); err != nil {
					return fuzzItem{}, err
				}
				payload := make([]byte, 0)
				if payloadLen > 0 {
					payload = make([]byte, int(payloadLen))
					if _, err := buf.Read(payload); err != nil {
						return fuzzItem{}, err
					}
				}
				return fuzzItem{
					Name:    string(nameBytes),
					Count:   int(count),
					Enabled: enabledByte == 1,
					Data:    payload,
				}, nil
			},
		)
		if len(name) > math.MaxInt32 || len(payload) > math.MaxInt32 || count > math.MaxInt32 || count < math.MinInt32 {
			t.Skip("skip oversized payloads for the fixed-width binary fuzz harness")
		}
		want := fuzzItem{Name: name, Count: count, Enabled: enabled, Data: []byte(payload)}
		encoded, err := codec.Encode(want)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		got, err := codec.Decode(encoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
		}
	})
}
