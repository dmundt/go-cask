# binary — compact custom payload codec

Package `binary` provides a generic `Codec[T]` for compact, caller-defined binary payloads in the `cas` core.

This package is intentionally object-agnostic. It does not encode `Blob`, `Tree`, or any other app-specific object model. Instead, the caller supplies the exact `marshal` and `unmarshal` functions for the value type they want to store, and the package applies them as a standard `Codec[T]`.

## Policy

- The `cas` core stays format-agnostic.
- This codec is for compact binary payloads when JSON would be too verbose.
- It is intended for explicit, versioned binary layouts chosen by the application, not for a single universal binary format.
- The object-specific schema stays in the app or gitlike layer; the generic codec does not know about object types.

## Typical use

```go
codec := binary.New[MyType](
    func(v MyType) ([]byte, error) { /* custom binary layout */ },
    func(data []byte) (MyType, error) { /* parse it back */ },
)
store := cas.New(raw, codec, sha256.New())
```

Use this when you need compact binary payloads and are comfortable defining a stable per-type binary schema and versioning strategy.

## Object-graph examples

This codec is designed for application-defined object graphs such as `Blob` and `Tree`.

```go
// Blob: version + mode + length-prefixed payload.
type Blob struct {
    Mode string
    Data []byte
}

// Tree: version + count + repeated entries (name, mode, child hash).
type TreeEntry struct {
    Name string
    Mode string
    Hash cas.Digest
}

type Tree struct {
    Entries []TreeEntry
}
```

The important rule is simple: the `cas` core stays agnostic, while the application chooses the binary wire format for each type and passes the marshal/unmarshal functions to `binary.New[T]`.

```go
codec := binary.New[Blob](marshalBlob, unmarshalBlob)
store := cas.New(raw, codec, sha256.New())
```

A tight tree example:

```go
func marshalTree(t Tree) ([]byte, error) {
    var b bytes.Buffer
    b.WriteByte(1) // version
    binary.Write(&b, binary.BigEndian, uint32(len(t.Entries)))
    for _, e := range t.Entries {
        binary.Write(&b, binary.BigEndian, uint32(len(e.Name)))
        b.WriteString(e.Name)
        binary.Write(&b, binary.BigEndian, uint32(len(e.Mode)))
        b.WriteString(e.Mode)
        b.Write(e.Hash[:])
    }
    return b.Bytes(), nil
}

func unmarshalTree(data []byte) (Tree, error) {
    if len(data) == 0 || data[0] != 1 {
        return Tree{}, fmt.Errorf("tree: bad version")
    }
    r := bytes.NewReader(data[1:])
    var n uint32
    binary.Read(r, binary.BigEndian, &n)
    out := make([]TreeEntry, 0, n)
    for i := uint32(0); i < n; i++ {
        var nameLen, modeLen uint32
        binary.Read(r, binary.BigEndian, &nameLen)
        name := make([]byte, nameLen)
        r.Read(name)
        binary.Read(r, binary.BigEndian, &modeLen)
        mode := make([]byte, modeLen)
        r.Read(mode)
        var h cas.Digest
        r.Read(h[:])
        out = append(out, TreeEntry{Name: string(name), Mode: string(mode), Hash: h})
    }
    return Tree{Entries: out}, nil
}
```

This pattern is the intended usage for compact, stable, versioned binary payloads without forcing object-specific logic into the `cas` core.
