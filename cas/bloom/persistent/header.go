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
//	9       1     header-checksum scheme: 0 = none (a file written before the
//	              checksum existed), 1 = CRC-64/ECMA over kind || key
//	10      6     the header checksum: the low six bytes of that CRC
//	16      32    index key — the material the default index hash is derived from
//	48      ...   the bitset
//
// The key is what makes the default index hash restart-stable. A Bloom index
// computed with a process-local seed (bloom.DefaultIndexHash) reports false for
// every digest another process recorded, and bloom.Guard treats a negative as
// authoritative absence — so a reopened filter would answer "absent" for objects
// that are stored, which is a wrong answer rather than a lost hint (#254).
//
// The checksum is what makes the key trustworthy. A key read without one is a
// reindexing waiting to happen: a single flipped bit — bit rot, or a filter file
// restored from elsewhere — sends every probe to the wrong slots, so every
// recorded digest reports absent and bloom.Guard turns that into an authoritative
// absence while the object is on disk (#361). The checksum covers the kind byte
// as well as the key, because a kind that flips between the two kinds this build
// knows is just as invisible to the kind check alone.
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
	// checksumSchemeCRC64 marks a header whose reserved bytes carry the
	// kind||key checksum described above. The scheme is persisted rather than
	// implied so a zero byte unambiguously means "written before the checksum
	// existed" (every header this package wrote before #361) and a wider or
	// different check can be added without guessing at an old file.
	checksumSchemeCRC64 byte = 1
	// checksumSize is the persisted checksum length in bytes. Six bytes of a
	// CRC-64 leave a 1-in-2^48 chance that a corrupted header still verifies,
	// which is far below the chance that a rebuilt hint set costs anything.
	checksumSize = 6
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
// headerSize bytes. The kind, the key and the checksum over them are written
// deterministically, so the same kind and key always produce the same bytes.
func encodeHeader(dst []byte, kind byte, key indexKey) {
	copy(dst[0:8], magic[:])
	dst[8] = kind
	dst[9] = checksumSchemeCRC64
	sum := headerChecksum(kind, key)
	copy(dst[10:16], sum[:])
	copy(dst[16:headerSize], key[:])
}

// headerChecksum returns the low checksumSize bytes of the CRC-64/ECMA checksum
// over the header's kind byte and index key.
//
// The kind is covered because both kinds this build knows are otherwise valid: a
// flipped kind byte alone would silently change how every recorded digest is
// indexed. The key is covered because it is the index itself — a changed key
// reindexes the whole bitset, so every recorded digest reports absent and
// bloom.Guard turns that miss into an authoritative absence (#361).
func headerChecksum(kind byte, key indexKey) [checksumSize]byte {
	var buf [1 + keySize]byte
	buf[0] = kind
	copy(buf[1:], key[:])
	var full [8]byte
	binary.LittleEndian.PutUint64(full[:], crc64.Checksum(buf[:], indexTable))
	var sum [checksumSize]byte
	copy(sum[:], full[:checksumSize])
	return sum
}

// decodeHeader reads the fixed header from src. ok is false when src carries no
// usable header — an empty file, a file written before the header existed, a
// header naming a kind this build does not know, a header with no checksum (a
// file written before #361) or one naming a checksum scheme this build does not
// know, and a header whose key does not match its checksum. The caller then
// rebuilds the file with a fresh key instead of trusting bits it cannot index:
// the key is the index, so accepting a changed one answers "absent" for stored
// objects rather than losing a hint (#254, #361).
func decodeHeader(src []byte) (kind byte, key indexKey, ok bool) {
	if len(src) < headerSize || !bytes.Equal(src[0:8], magic[:]) {
		return 0, indexKey{}, false
	}
	kind = src[8]
	if kind != hashKindDefault && kind != hashKindCustom {
		return 0, indexKey{}, false
	}
	if src[9] != checksumSchemeCRC64 {
		return 0, indexKey{}, false
	}
	copy(key[:], src[16:headerSize])
	want := headerChecksum(kind, key)
	if !bytes.Equal(want[:], src[10:16]) {
		return 0, indexKey{}, false
	}
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
