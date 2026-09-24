package persistent

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc64"

	"github.com/dmundt/go-cask/cas/bloom"
)

// The persistent file is a fixed header followed by the Bloom bitset:
//
//	offset  size  contents
//	0       8     magic, "CASKBLM1" (the last byte is the format version)
//	8       1     index-hash kind: 0 = this package's default, 1 = caller-supplied
//	9       7     reserved, zero; a reader MUST ignore it
//	16      32    index key — the material the default index hash is derived from
//	48      ...   the bitset
//
// The key is what makes the default index hash restart-stable. A Bloom index
// computed with a process-local seed (bloom.DefaultIndexHash) reports false for
// every digest another process recorded, and bloom.Guard treats a negative as
// authoritative absence — so a reopened filter would answer "absent" for objects
// that are stored, which is a wrong answer rather than a lost hint (#254).
const (
	// headerSize is the fixed header length in bytes; the bitset follows it.
	headerSize = 48
	// keySize is the persisted index-key length.
	keySize = 32
	// hashKindDefault selects the package's keyed, restart-stable index hash.
	hashKindDefault byte = 0
	// hashKindCustom records that the file was written with a caller-supplied
	// Config.Hash. The kind is recorded so reopening with the other kind rebuilds
	// the hint set instead of trusting bits indexed under a different rule.
	hashKindCustom byte = 1
)

// magic is the file signature: seven ASCII bytes plus the format version.
var magic = [8]byte{'C', 'A', 'S', 'K', 'B', 'L', 'M', '1'}

// indexTable is the fixed polynomial the default index hash uses. It is built
// once and read-only, so it is not mutable state a caller could observe.
var indexTable = crc64.MakeTable(crc64.ECMA)

// indexKey is the persisted material the default index hash is derived from. It
// is generated once per file and reused on every reopen, which is what keeps the
// bit positions stable across processes.
type indexKey [keySize]byte

// randomKey returns a fresh index key from the system random source.
func randomKey() (indexKey, error) {
	var key indexKey
	if _, err := rand.Read(key[:]); err != nil {
		return indexKey{}, fmt.Errorf("bloom/persistent: read index key: %w", err)
	}
	return key, nil
}

// encodeHeader writes the fixed header into dst, which must be at least
// headerSize bytes. The reserved bytes are written as zeros so the file is
// deterministic apart from its key.
func encodeHeader(dst []byte, kind byte, key indexKey) {
	copy(dst[0:8], magic[:])
	dst[8] = kind
	clear(dst[9:16])
	copy(dst[16:headerSize], key[:])
}

// decodeHeader reads the fixed header from src. ok is false when src carries no
// usable header — an empty file, a file written before the header existed, or a
// header naming a kind this build does not know — and the caller then rebuilds
// the file with a fresh key instead of trusting bits it cannot index.
func decodeHeader(src []byte) (kind byte, key indexKey, ok bool) {
	if len(src) < headerSize || !bytes.Equal(src[0:8], magic[:]) {
		return 0, indexKey{}, false
	}
	kind = src[8]
	if kind != hashKindDefault && kind != hashKindCustom {
		return 0, indexKey{}, false
	}
	copy(key[:], src[16:headerSize])
	return kind, key, true
}

// keyedIndexHash returns the default index hash: deterministic for a given key,
// so a bitset stays valid across processes and restarts. Every probe appends its
// own index to the hashed input, so one digest's k positions stay distinct
// instead of collapsing onto one another.
//
// The key's contribution is folded into a starting checksum once, outside the
// closure: CRC-64 chaining is associative, so starting from the key's checksum
// gives the same positions as hashing key||data||probe while leaving only the
// digest and the probe to hash on the hot path.
func keyedIndexHash(key indexKey) bloom.IndexHash {
	keySum := crc64.Update(0, indexTable, key[:])
	return func(data []byte, i int) uint64 {
		var probe [8]byte
		binary.LittleEndian.PutUint64(probe[:], uint64(i))
		sum := crc64.Update(keySum, indexTable, data)
		return crc64.Update(sum, indexTable, probe[:])
	}
}
