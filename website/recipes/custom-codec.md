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
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte) (T, error)
}
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

var _ cas.Codec[*Note] = Codec{}
```

## Composing with a compression wrapper

`cas/codec/gzip`, `cas/codec/zlib`, and `cas/codec/flate` each wrap an inner
`Codec[T]`: they run the inner codec first, then compress the result.

```go
inner := notecodec.New()
compressed := gzip.New[*Note](inner)

data, _ := compressed.Encode(&Note{Text: "hello world"})
note, _ := compressed.Decode(data)
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
