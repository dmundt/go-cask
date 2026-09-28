# crc64

CRC-64/ECMA-182 `cas.Hasher` helper. Its 8-byte digest is twice the width of `crc32` and `adler32`, so choose it when the checksum is a large or long-lived store's only integrity signal; `crc32` when interoperability matters, `adler32` when computation cost does ([selection rule](../README.md#choosing-among-the-three)), which also decides a sidecar record's algorithm.
