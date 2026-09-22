// Package cbor provides a compact CBOR codec layer for CAS payloads.
//
// The package follows the repo's codec-stack model: a CBOR codec can wrap an
// inner codec and also define the explicit value-to-bytes conversion for the
// concrete T being serialized.
package cbor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/dmundt/go-cask/cas"
)

// Codec encodes and decodes values using a compact CBOR subset without runtime
// reflection. The caller decides the exact value shape for each concrete T and
// provides the small encode/decode functions that map T to/from the supported
// scalar, array, string, byte-string, and map forms. It may also wrap an inner
// codec when the payload should pass through a lower layer first.
type Codec[T any] struct {
	next   cas.Codec[T]
	encode func(T) ([]byte, error)
	decode func([]byte) (T, error)
}

var (
	errNilEncode = errors.New("cbor: encode func is nil")
	errNilDecode = errors.New("cbor: decode func is nil")
)

// New wraps an inner codec and preserves the repo's stack model: the next codec
// defines the inner serialization, while this CBOR codec acts as the outermost
// representational layer. The caller still supplies the CBOR conversion logic.
func New[T any](next cas.Codec[T], encode func(T) ([]byte, error), decode func([]byte) (T, error)) Codec[T] {
	return Codec[T]{next: next, encode: encode, decode: decode}
}

// NewRaw creates a direct CBOR codec for a concrete T using explicit conversion
// functions; it does not depend on an inner codec.
func NewRaw[T any](encode func(T) ([]byte, error), decode func([]byte) (T, error)) Codec[T] {
	return Codec[T]{encode: encode, decode: decode}
}

// NewValue creates a CBOR codec for the generic any/value model used by small
// metadata and manifest payloads. Values are limited to the scalar, array, map,
// bytes, and string shapes supported by this package.
func NewValue() Codec[any] {
	return NewRaw[any](encodeAny, decodeAny)
}

// NewMap creates a codec for map[string]any payloads with deterministic key ordering.
func NewMap() Codec[map[string]any] {
	return NewRaw[map[string]any](encodeMapValue, decodeMapValue)
}

// Encode encodes v using the configured CBOR conversion functions.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next != nil && c.encode == nil {
		return c.next.Encode(v)
	}
	if c.encode == nil {
		return nil, errNilEncode
	}
	return c.encode(v)
}

// Decode decodes data using the configured CBOR conversion functions.
func (c Codec[T]) Decode(data []byte) (T, error) {
	if c.next != nil && c.decode == nil {
		return c.next.Decode(data)
	}
	if c.decode == nil {
		var zero T
		return zero, errNilDecode
	}
	return c.decode(data)
}

func encodeAny(v any) ([]byte, error) {
	return appendEncodedValue(nil, v)
}

func appendEncodedValue(dst []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(dst, 0xf6), nil
	case bool:
		if x {
			return append(dst, 0xf5), nil
		}
		return append(dst, 0xf4), nil
	case int:
		return encodeInt64Into(dst, int64(x))
	case int8:
		return encodeInt64Into(dst, int64(x))
	case int16:
		return encodeInt64Into(dst, int64(x))
	case int32:
		return encodeInt64Into(dst, int64(x))
	case int64:
		return encodeInt64Into(dst, x)
	case uint:
		return encodeUint64Into(dst, uint64(x))
	case uint8:
		return encodeUint64Into(dst, uint64(x))
	case uint16:
		return encodeUint64Into(dst, uint64(x))
	case uint32:
		return encodeUint64Into(dst, uint64(x))
	case uint64:
		return encodeUint64Into(dst, x)
	case float32:
		return encodeFloat64Into(dst, float64(x))
	case float64:
		return encodeFloat64Into(dst, x)
	case string:
		return appendStringBytes(dst, []byte(x)), nil
	case []byte:
		return appendBytes(dst, x), nil
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return appendArrayValue(dst, items)
	case []any:
		return appendArrayValue(dst, x)
	case map[string]any:
		return appendMapValue(dst, x)
	case map[string]string:
		items := make(map[string]any, len(x))
		for k, v := range x {
			items[k] = v
		}
		return appendMapValue(dst, items)
	default:
		return nil, fmt.Errorf("cbor: unsupported value type %T", v)
	}
}

func encodeMapValue(v map[string]any) ([]byte, error) {
	return appendMapValue(nil, v)
}

func decodeAny(data []byte) (any, error) {
	value, rest, err := decodeOne(data)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("cbor: trailing data")
	}
	return value, nil
}

func appendMapValue(dst []byte, v map[string]any) ([]byte, error) {
	if v == nil {
		return appendMajor(dst, 5, 0), nil
	}
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	dst = appendMajor(dst, 5, uint64(len(keys)))
	for _, key := range keys {
		dst = appendStringBytes(dst, []byte(key))
		var err error
		dst, err = appendEncodedValue(dst, v[key])
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func decodeMapValue(data []byte) (map[string]any, error) {
	value, err := decodeAny(data)
	if err != nil {
		return nil, err
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cbor: expected map[string]any, got %T", value)
	}
	return m, nil
}

func appendArrayValue(dst []byte, items []any) ([]byte, error) {
	dst = appendMajor(dst, 4, uint64(len(items)))
	for _, item := range items {
		var err error
		dst, err = appendEncodedValue(dst, item)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func appendBytes(dst []byte, data []byte) []byte {
	dst = appendMajor(dst, 2, uint64(len(data)))
	return append(dst, data...)
}

func appendStringBytes(dst []byte, data []byte) []byte {
	dst = appendMajor(dst, 3, uint64(len(data)))
	return append(dst, data...)
}

func encodeInt64Into(dst []byte, v int64) ([]byte, error) {
	if v >= 0 {
		return appendMajor(dst, 0, uint64(v)), nil
	}
	return appendMajor(dst, 1, uint64(-(v + 1))), nil
}

func encodeUint64Into(dst []byte, v uint64) ([]byte, error) {
	return appendMajor(dst, 0, v), nil
}

func encodeFloat64Into(dst []byte, v float64) ([]byte, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("cbor: unsupported float value %v", v)
	}
	bits := math.Float64bits(v)
	buf := make([]byte, 8)
	for i := range buf {
		buf[i] = byte(bits >> (8 * (7 - i)))
	}
	return append(append(dst, 0xfb), buf...), nil
}

func appendMajor(dst []byte, mt byte, length uint64) []byte {
	if length < 24 {
		return append(dst, byte(mt<<5)|byte(length))
	}
	if length <= math.MaxUint8 {
		return append(dst, byte(mt<<5)|24, byte(length))
	}
	if length <= math.MaxUint16 {
		return append(dst,
			byte(mt<<5)|25,
			byte(length>>8),
			byte(length),
		)
	}
	if length <= math.MaxUint32 {
		return append(dst,
			byte(mt<<5)|26,
			byte(length>>24),
			byte(length>>16),
			byte(length>>8),
			byte(length),
		)
	}
	return append(dst,
		byte(mt<<5)|27,
		byte(length>>56),
		byte(length>>48),
		byte(length>>40),
		byte(length>>32),
		byte(length>>24),
		byte(length>>16),
		byte(length>>8),
		byte(length),
	)
}

func decodeOne(data []byte) (any, []byte, error) {
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("cbor: unexpected end of input")
	}
	b := data[0]
	major := b >> 5
	addl := b & 0x1f

	if major == 7 {
		switch addl {
		case 20:
			return false, data[1:], nil
		case 21:
			return true, data[1:], nil
		case 22:
			return nil, data[1:], nil
		case 25:
			if len(data) < 3 {
				return nil, nil, fmt.Errorf("cbor: truncated float16")
			}
			bits := binary.BigEndian.Uint16(data[1:3])
			return float64(math.Float32frombits(uint32(bits))), data[3:], nil
		case 26:
			if len(data) < 5 {
				return nil, nil, fmt.Errorf("cbor: truncated float32")
			}
			bits := binary.BigEndian.Uint32(data[1:5])
			return float64(math.Float32frombits(bits)), data[5:], nil
		case 27:
			if len(data) < 9 {
				return nil, nil, fmt.Errorf("cbor: truncated float64")
			}
			return math.Float64frombits(binary.BigEndian.Uint64(data[1:9])), data[9:], nil
		default:
			return nil, nil, fmt.Errorf("cbor: unsupported simple value %d", addl)
		}
	}

	pos := 1
	length, err := readLength(data[1:], &pos, addl)
	if err != nil {
		return nil, nil, err
	}
	headerLen := pos
	payloadStart := headerLen

	switch major {
	case 0:
		return int64(length), data[headerLen:], nil
	case 1:
		return -1 - int64(length), data[headerLen:], nil
	case 2:
		// The declared length is untrusted: reject it before converting to int
		// or slicing, otherwise a huge value overflows and panics.
		if length > uint64(len(data)-payloadStart) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		payloadEnd := payloadStart + int(length)
		return data[payloadStart:payloadEnd], data[payloadEnd:], nil
	case 3:
		if length > uint64(len(data)-payloadStart) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		payloadEnd := payloadStart + int(length)
		return string(data[payloadStart:payloadEnd]), data[payloadEnd:], nil
	case 4:
		// Every item occupies at least one byte, so a declared count larger
		// than the bytes remaining is malformed. Rejecting it before the loop
		// keeps the count from sizing any allocation.
		if length > uint64(len(data)-payloadStart) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		items := make([]any, 0)
		cursor := payloadStart
		for i := uint64(0); i < length; i++ {
			item, rest, err := decodeOne(data[cursor:])
			if err != nil {
				return nil, nil, err
			}
			items = append(items, item)
			cursor = len(data) - len(rest)
		}
		return items, data[cursor:], nil
	case 5:
		// Same guard as arrays; a pair needs at least two bytes, but one byte
		// per entry is already enough to bound the decode by the input size.
		if length > uint64(len(data)-payloadStart) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		m := make(map[string]any)
		cursor := payloadStart
		for i := uint64(0); i < length; i++ {
			key, rest, err := decodeOne(data[cursor:])
			if err != nil {
				return nil, nil, err
			}
			keyStr, ok := key.(string)
			if !ok {
				return nil, nil, fmt.Errorf("cbor: map key must be a string")
			}
			value, rest2, err := decodeOne(rest)
			if err != nil {
				return nil, nil, err
			}
			m[keyStr] = value
			cursor = len(data) - len(rest2)
		}
		return m, data[cursor:], nil
	default:
		return nil, nil, fmt.Errorf("cbor: unsupported major type %d", major)
	}
}

func readLength(data []byte, pos *int, ai byte) (uint64, error) {
	if ai < 24 {
		return uint64(ai), nil
	}
	if ai == 24 {
		if len(data) < 1 {
			return 0, fmt.Errorf("cbor: truncated length")
		}
		*pos += 1
		return uint64(data[0]), nil
	}
	if ai == 25 {
		if len(data) < 2 {
			return 0, fmt.Errorf("cbor: truncated length")
		}
		*pos += 2
		return uint64(data[0])<<8 | uint64(data[1]), nil
	}
	if ai == 26 {
		if len(data) < 4 {
			return 0, fmt.Errorf("cbor: truncated length")
		}
		*pos += 4
		return uint64(data[0])<<24 | uint64(data[1])<<16 | uint64(data[2])<<8 | uint64(data[3]), nil
	}
	if ai == 27 {
		if len(data) < 8 {
			return 0, fmt.Errorf("cbor: truncated length")
		}
		*pos += 8
		return uint64(data[0])<<56 | uint64(data[1])<<48 | uint64(data[2])<<40 | uint64(data[3])<<32 |
			uint64(data[4])<<24 | uint64(data[5])<<16 | uint64(data[6])<<8 | uint64(data[7]), nil
	}
	return 0, fmt.Errorf("cbor: unsupported length encoding %d", ai)
}
