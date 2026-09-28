# adler32

Adler-32 (RFC 1950, zlib's checksum) `cas.Hasher` helper. Cheapest of the three shipped checksums and the weakest: computed from two modulo-65521 sums, so it needs no polynomial table. Choose it when the corruption to catch is accidental and the computation cost matters; `crc32` for interoperability, `crc64` for a wider digest ([selection rule](../README.md#choosing-among-the-three)), which also decides a sidecar record's algorithm.
