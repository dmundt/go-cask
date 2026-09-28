# hash

Client-side algorithm seam for the generic `cas` core: raw digests stored, no algorithm named; the
caller injects a `cas.Hasher` when building a store. Changing the algorithm is a format transition,
not a configuration change — `cas` imports no concrete hasher (`docs/specs/cas-core.md` §4.2;
`docs/specs/defaults.md` §2).

## Included implementations

- [sha256](./sha256/README.md) — recommended default for new durable CAS data
- [sha512](./sha512/README.md) — full-width standard-library SHA-512 option
- [sha512_256](./sha512_256/README.md) — fast secure alternative with a 256-bit output size

Each leaf owns its `Name`/`Size` and the shared digest helpers `hash.FormatDigest`,
`hash.ParseDigest` (rejects another algorithm's prefix with `cas.ErrInvalidDigest`) and
`hash.ValidateDigestSize`. Display form: `cas.Digest.Prefix(n)`, the first `n` hex characters
(§4.1) — the digest carries no algorithm.
