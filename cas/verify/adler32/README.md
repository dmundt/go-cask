# adler32

`adler32` is an Adler-32 (RFC 1950, zlib's checksum) helper for go-cask. It implements `cas.Hasher`, so it can be the hasher a store is addressed with — and then verifies those same objects; it cannot validate an object addressed by another algorithm ([the constraint](../README.md#the-constraint-these-helpers-live-under)). It can also be the checksum a [`sidecar`](../sidecar/README.md) record stores beside a strongly-addressed object, which is the cheap-check path.

Adler-32 is computed from two modulo-65521 sums and needs no polynomial table, which makes it the cheapest of the three shipped checksums and the weakest of them. Choose it when the corruption to catch is accidental and the computation cost matters; choose `crc32` for interoperability and `crc64` for a wider digest ([selection rule](../README.md#choosing-among-the-three)). The same rule decides a sidecar record's algorithm.
