package cas

import "encoding/json"

// Codec[T] serializes typed values to and from bytes. The contract:
// Decode(Encode(v)) == v for all storable values (round-trip). The default
// is JSONCodec[T]; compression, encryption or protobuf codecs are additional
// Codec[T] implementations and never change the byte layer.
type Codec[T any] interface {
	Encode(v T) ([]byte, error)
	Decode(data []byte) (T, error)
}

// JSONCodec[T] is the default Codec[T]: encoding/json Marshal/Unmarshal.
type JSONCodec[T any] struct{}

// Encode marshals v to JSON.
func (JSONCodec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode unmarshals JSON into a fresh T.
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
