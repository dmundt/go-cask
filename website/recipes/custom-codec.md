# Custom codec recipe

A custom codec is a good fit when your application wants a compact or domain-specific representation.

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
- the app owns the representation policy
- you can stack wrapping codecs and compression on top of each other

## Envelope design

Keep the byte-level contract explicit. The storage model should remain stable even when the application representation changes.
