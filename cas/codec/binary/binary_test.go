package binary

import (
	"bytes"
	"reflect"
	"testing"
)

type sample struct {
	Name    string
	Count   int
	Enabled bool
	Data    []byte
}

func TestCodecRoundTrip(t *testing.T) {
	codec := New(
		func(v sample) ([]byte, error) {
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
		func(data []byte) (sample, error) {
			buf := bytes.NewBuffer(data)
			version, err := buf.ReadByte()
			if err != nil || version != 1 {
				return sample{}, err
			}
			nameLen, err := buf.ReadByte()
			if err != nil {
				return sample{}, err
			}
			name := make([]byte, nameLen)
			if _, err := buf.Read(name); err != nil {
				return sample{}, err
			}
			count, err := buf.ReadByte()
			if err != nil {
				return sample{}, err
			}
			enabledByte, err := buf.ReadByte()
			if err != nil {
				return sample{}, err
			}
			dataLen, err := buf.ReadByte()
			if err != nil {
				return sample{}, err
			}
			payload := make([]byte, dataLen)
			if _, err := buf.Read(payload); err != nil {
				return sample{}, err
			}
			return sample{
				Name:    string(name),
				Count:   int(count),
				Enabled: enabledByte == 1,
				Data:    payload,
			}, nil
		},
	)

	want := sample{Name: "demo", Count: 7, Enabled: true, Data: []byte{0x1, 0x2, 0x3}}
	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}
