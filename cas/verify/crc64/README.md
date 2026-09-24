# crc64

`crc64` is a CRC-64/ECMA-182 checksum helper for go-cask. It implements `cas.Hasher`, so it can be the hasher a store is addressed with — and then verifies those same objects; it cannot validate an object addressed by another algorithm ([the constraint](../README.md#the-constraint-these-helpers-live-under)).

Its 8-byte digest is twice the width of `crc32` and `adler32`, so it is the one to choose when the checksum is a large or long-lived store's only integrity signal; use `crc32` when interoperability matters and `adler32` when computation cost does ([selection rule](../README.md#choosing-among-the-three)).
