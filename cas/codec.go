package cas

// Codec[T] serializes typed values to and from bytes. The contract:
// Decode(Encode(v)) == v for all storable values (round-trip). Concrete
// implementations live in the cas/codec subpackage (JSONCodec, GobCodec,
// and any app-defined codecs) and never change the byte layer.
type Codec[T any] interface {
	Encode(v T) ([]byte, error)
	Decode(data []byte) (T, error)
}
