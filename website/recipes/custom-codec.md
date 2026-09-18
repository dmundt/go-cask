# Custom codec recipe

A custom codec is useful when your application wants a compact or domain-specific representation without changing the storage contract.

## When to use it

- the native object format is not a good fit for JSON
- payload size matters
- you want a stable binary wire format for a domain model
- you want to layer compression or encryption on top of an existing codec

## Pattern

```go
package mycodec

type Codec struct{}

func New() *Codec { return &Codec{} }

func (c *Codec) Encode(v MyType) ([]byte, error) {
    return []byte(v.String()), nil
}

func (c *Codec) Decode(data []byte) (MyType, error) {
    return MyType(data), nil
}
```

## Why this helps

- the storage layer stays unchanged
- the application owns the representation policy
- wrapping codecs can be composed cleanly

## Envelope design

Keep the byte-level contract explicit. The storage model should remain stable even as the application representation changes.
