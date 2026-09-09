package cas

// Codec[T] serializes typed values to and from bytes. The contract:
// Unmarshal(Marshal(v)) == v for all storable values (round-trip). Concrete
// implementations live in the cas/codec subpackage (JSONCodec, GobCodec,
// and any app-defined codecs) and never change the byte layer.
type Codec[T any] interface {
	Marshal(v T) ([]byte, error)
	Unmarshal(data []byte) (T, error)
}
