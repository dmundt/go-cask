# Custom codec recipe

A custom codec is useful when JSON is not the right shape for your payload,
or you want a compact, domain-specific wire format without changing the
storage contract.

## When to use it

- the native JSON representation is not a good fit for the payload
- payload size matters and a general-purpose binary/CBOR codec is not
  specific enough
- you want a stable, hand-written wire format for one domain type

## The contract

```go
package codec

import "github.com/dmundt/go-cask/cas"

// Codec is the two-method serialization contract a custom codec implements.
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte) (T, error)
}

// The contract above and the shipped cas.Codec accept exactly each other's
// implementations, so this listing cannot drift from the real interface.
var (
    _ cas.Codec[string] = Codec[string](nil)
    _ Codec[string]     = cas.Codec[string](nil)
)
```

`Decode(Encode(v))` must equal `v` for every storable value.

## A minimal example

```go
package notecodec

import "github.com/dmundt/go-cask/cas"

type Note struct {
    Text string
}

// Codec implements cas.Codec[*Note] with the simplest possible wire format:
// the text itself, as raw bytes.
type Codec struct{}

func New() Codec { return Codec{} }

func (Codec) Encode(v *Note) ([]byte, error) {
    return []byte(v.Text), nil
}

func (Codec) Decode(data []byte) (*Note, error) {
    return &Note{Text: string(data)}, nil
}

// CodecName declares the wire format, so the store can report a codec change
// as cas.ErrCodecMismatch instead of a decode failure (cas.CodecNamer).
func (Codec) CodecName() string { return "notecodec" }

var _ cas.Codec[*Note] = Codec{}
var _ cas.CodecNamer = Codec{}
```

## Composing with a compression wrapper

`cas/codec/gzip`, `cas/codec/zlib`, and `cas/codec/flate` each wrap an inner
`Codec[T]`: they run the inner codec first, then compress the result. Swap
`jsoncodec` below for the `notecodec` package above to compress that format
instead of JSON:

```go
package example

import (
    "fmt"

    "github.com/dmundt/go-cask/cas/codec/gzip"
    jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type Note struct {
    Text string `json:"text"`
}

func wrap() {
    compressed := gzip.New(jsoncodec.New[*Note]())
    data, _ := compressed.Encode(&Note{Text: "hello world"})
    note, _ := compressed.Decode(data)
    fmt.Println(note.Text, len(data))
}
```

## Why this helps

- the storage layer and `Backend` never change
- the application owns the representation policy end to end
- wrapper codecs compose without touching the inner codec's code

## Identity note

The digest a `Store[T]` computes covers the codec's output (wrapped in the
object envelope — see [object format](../specifications/object-format.md)).
Changing the codec, or adding/removing a compression wrapper, changes the
digest of an otherwise-identical value: it is a different stored
representation, and the store treats it as a different object.

The envelope also records the writing codec's identity tag. A codec that
implements `cas.CodecNamer` has its tag written into every object it stores,
and `Store.Get` compares it with its own codec's tag before decoding: reading
an object written with another codec returns `cas.ErrCodecMismatch` — a
reported format change, not `ErrCorrupt` — so a codec swap needs no type major
bump. Leave the interface off and the tag is empty, which means "unspecified"
and disables the check.
