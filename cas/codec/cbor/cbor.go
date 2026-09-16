package cbor

import (
	"encoding/binary"
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
	errNilEncode = fmt.Errorf("cbor: encode func is nil")
	errNilDecode = fmt.Errorf("cbor: decode func is nil")
)

// New wraps an inner codec and preserves the repo's stack model: the next codec
// defines the inner serialization, while this CBOR codec acts as the outermost
// representational layer. The caller still supplies the CBOR conversion logic.
func New[T any](next cas.Codec[T], encode func(T) ([]byte, error), decode func([]byte) (T, error)) Codec[T] {
	return Codec[T]{next: next, encode: encode, decode: decode}
}

// NewWithNext is the explicit wrapper constructor for CBOR stacks; it mirrors
// the repo's next-first chaining convention for codec wrappers.
func NewWithNext[T any](next cas.Codec[T], encode func(T) ([]byte, error), decode func([]byte) (T, error)) Codec[T] {
	return New(next, encode, decode)
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

func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next != nil && c.encode == nil {
		return c.next.Encode(v)
	}
	if c.encode == nil {
		return nil, errNilEncode
	}
	return c.encode(v)
}

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

func estimateAnySize(v any) (int, error) {
	switch x := v.(type) {
	case nil:
		return 1, nil
	case bool:
		return 1, nil
	case int:
		if x >= 0 {
			return lenMajorHeader(0, uint64(x)), nil
		}
		return lenMajorHeader(1, uint64(-(x + 1))), nil
	case int8:
		if x >= 0 {
			return lenMajorHeader(0, uint64(x)), nil
		}
		return lenMajorHeader(1, uint64(-(x + 1))), nil
	case int16:
		if x >= 0 {
			return lenMajorHeader(0, uint64(x)), nil
		}
		return lenMajorHeader(1, uint64(-(x + 1))), nil
	case int32:
		if x >= 0 {
			return lenMajorHeader(0, uint64(x)), nil
		}
		return lenMajorHeader(1, uint64(-(x + 1))), nil
	case int64:
		if x >= 0 {
			return lenMajorHeader(0, uint64(x)), nil
		}
		return lenMajorHeader(1, uint64(-(x + 1))), nil
	case uint:
		return lenMajorHeader(0, uint64(x)), nil
	case uint8:
		return lenMajorHeader(0, uint64(x)), nil
	case uint16:
		return lenMajorHeader(0, uint64(x)), nil
	case uint32:
		return lenMajorHeader(0, uint64(x)), nil
	case uint64:
		return lenMajorHeader(0, x), nil
	case float32:
		return 9, nil
	case float64:
		return 9, nil
	case string:
		return lenMajorHeader(3, uint64(len(x))) + len(x), nil
	case []byte:
		return lenMajorHeader(2, uint64(len(x))) + len(x), nil
	case []string:
		total := lenMajorHeader(4, uint64(len(x)))
		for _, s := range x {
			total += lenMajorHeader(3, uint64(len(s))) + len(s)
		}
		return total, nil
	case []any:
		total := lenMajorHeader(4, uint64(len(x)))
		for _, item := range x {
			sz, err := estimateAnySize(item)
			if err != nil {
				return 0, err
			}
			total += sz
		}
		return total, nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		total := lenMajorHeader(5, uint64(len(keys)))
		for _, key := range keys {
			encodedKey := appendMajor(nil, 3, uint64(len(key)))
			total += len(encodedKey) + len(key)
			sz, err := estimateAnySize(x[key])
			if err != nil {
				return 0, err
			}
			total += sz
		}
		return total, nil
	case map[string]string:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		total := lenMajorHeader(5, uint64(len(keys)))
		for _, key := range keys {
			encodedKey := appendMajor(nil, 3, uint64(len(key)))
			total += len(encodedKey) + len(key)
			total += lenMajorHeader(3, uint64(len(x[key]))) + len(x[key])
		}
		return total, nil
	default:
		return 0, fmt.Errorf("cbor: unsupported value type %T", v)
	}
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

func encodeArray(items []any) ([]byte, error) {
	totalSize := lenMajorHeader(4, uint64(len(items)))
	for _, item := range items {
		sz, err := estimateAnySize(item)
		if err != nil {
			return nil, err
		}
		totalSize += sz
	}
	out := make([]byte, 0, totalSize)
	return appendArrayValue(out, items)
}

func encodeBytes(data []byte) ([]byte, error) {
	return appendBytes(nil, data), nil
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

func encodeStringBytes(data []byte) ([]byte, error) {
	out := appendMajor(nil, 3, uint64(len(data)))
	return append(out, data...), nil
}

func encodeInt64(v int64) ([]byte, error) {
	if v >= 0 {
		return appendMajor(nil, 0, uint64(v)), nil
	}
	return appendMajor(nil, 1, uint64(-(v + 1))), nil
}

func encodeUint64(v uint64) ([]byte, error) {
	return appendMajor(nil, 0, v), nil
}

func encodeFloat64(v float64) ([]byte, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("cbor: unsupported float value %v", v)
	}
	bits := math.Float64bits(v)
	buf := make([]byte, 8)
	for i := range buf {
		buf[i] = byte(bits >> (8 * (7 - i)))
	}
	return append([]byte{0xfb}, buf...), nil
}

func lenMajorHeader(mt byte, length uint64) int {
	if length < 24 {
		return 1
	}
	if length <= math.MaxUint8 {
		return 2
	}
	if length <= math.MaxUint16 {
		return 3
	}
	if length <= math.MaxUint32 {
		return 5
	}
	return 9
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
		payloadEnd := payloadStart + int(length)
		if payloadEnd > len(data) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		return data[payloadStart:payloadEnd], data[payloadEnd:], nil
	case 3:
		payloadEnd := payloadStart + int(length)
		if payloadEnd > len(data) {
			return nil, nil, fmt.Errorf("cbor: truncated value")
		}
		return string(data[payloadStart:payloadEnd]), data[payloadEnd:], nil
	case 4:
		items := make([]any, 0, int(length))
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
		m := make(map[string]any, int(length))
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
