# codec

The codec layer defines how typed values are serialized before they are stored and recovered after they are read. The `cas` core stays format-agnostic; the caller picks the codec.

## Included implementations

- [json](./json/README.md) — recommended default for readable, portable data
- [gob](./gob/README.md) — Go-only compatibility codec

## Policy

- Keep the core format-agnostic.
- Prefer JSON for durable, portable object data.
- Treat gob as a compatibility option for Go-only workflows.
- Treat compression-encryption wrappers as transport or storage layers, not as the canonical object format.

## Typical use

- Use JSON for readable object payloads and easy interop.
- Use gob only for explicit Go-to-Go compatibility or migration cases.
- Keep custom codecs explicit when the app needs a different binary or domain-specific format.

## Notes

A good codec matches the app's durability, portability, and compatibility requirements. A codec is not the right default if it is Go-only, unstable across versions, or unsuitable for long-lived object identity.
