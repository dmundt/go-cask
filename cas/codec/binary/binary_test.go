package binary

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type sample struct {
	Name    string
	Count   int
	Enabled bool
	Data    []byte
}

func TestCodecRoundTrip(t *testing.T) {
	codec := NewRaw(
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
	data, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecCascadeRoundTrip(t *testing.T) {
	codec := New(jsoncodec.New[sample](), func(data []byte) ([]byte, error) {
		return append([]byte("BIN:"), data...), nil
	}, func(data []byte) ([]byte, error) {
		if !bytes.HasPrefix(data, []byte("BIN:")) {
			return nil, errors.New("missing binary prefix")
		}
		return data[4:], nil
	})

	want := sample{Name: "demo", Count: 7, Enabled: true, Data: []byte{0x1, 0x2, 0x3}}
	data, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("BIN:")) {
		t.Fatalf("wrapped payload missing prefix: %q", data)
	}

	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecErrorsWhenCallbacksMissing(t *testing.T) {
	codec := New[sample](nil, nil, nil)
	if _, err := codec.Encode(sample{Name: "demo"}); err == nil {
		t.Fatal("Encode with nil function returned nil error, want non-nil")
	}
	if _, err := codec.Decode([]byte("bad")); err == nil {
		t.Fatal("Decode with nil function returned nil error, want non-nil")
	}

	wrapped := New(jsoncodec.New[sample](), nil, nil)
	if _, err := wrapped.Encode(sample{Name: "demo"}); err == nil {
		t.Fatal("wrapped Encode with nil transform should error")
	}
	if _, err := wrapped.Decode([]byte("bad")); err == nil {
		t.Fatal("wrapped Decode with nil restore should error")
	}

	restoreErr := New(jsoncodec.New[sample](), func(data []byte) ([]byte, error) { return data, nil }, func(data []byte) ([]byte, error) { return nil, errors.New("restore boom") })
	if _, err := restoreErr.Decode([]byte("bad")); err == nil {
		t.Fatal("restore failure should propagate")
	}
	transformErr := New(jsoncodec.New[sample](), func(data []byte) ([]byte, error) { return nil, errors.New("transform boom") }, func(data []byte) ([]byte, error) { return data, nil })
	if _, err := transformErr.Encode(sample{Name: "demo"}); err == nil {
		t.Fatal("transform failure should propagate")
	}
}
