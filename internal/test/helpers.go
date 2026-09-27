package test

import (
	"bytes"
	"encoding/binary"
	"io"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// This file holds the helpers the module's test suites share, so a fixture
// exists once instead of once per package that needs it. Everything here is
// test-only: the package is imported from _test.go files and the production
// tree never depends on it.

// DigestData computes the content address of data with the shipped sha256
// hasher, the algorithm go-cask's own clients wire.
func DigestData(data []byte) cas.Digest {
	return sha256.Of(data)
}

// ReadAllAndClose reads all bytes from rc and closes it.
func ReadAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

// MustDigest parses the hex form of a digest for a test fixture, failing the
// test when it is not a valid digest.
func MustDigest(t *testing.T, hexDigest string) cas.Digest {
	t.Helper()
	d, err := cas.ParseDigest(hexDigest)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// DigestKeys returns the digests' printable keys in ascending order, the form a
// test compares two listings as sets in.
func DigestKeys(digests []cas.Digest) []string {
	keys := make([]string, 0, len(digests))
	for _, d := range digests {
		keys = append(keys, d.String())
	}
	slices.Sort(keys)
	return keys
}

// ContainsDigest reports whether digests holds d.
func ContainsDigest(digests []cas.Digest, d cas.Digest) bool {
	for _, other := range digests {
		if other.Equal(d) {
			return true
		}
	}
	return false
}

// TLVEnvelope builds a version 1 TLV envelope over payload:
// [version u8 = 1][uvarint typeLen][type][uvarint payloadLen][payload]. Version
// 1 frames carry no codec identity, so they are the back-compatibility fixture
// the production writer (cas.EncodeEnvelope) no longer produces and every reader
// must still accept.
func TLVEnvelope(typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // envelopeVersionV1
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
}

// V2Envelope builds a version 2 TLV envelope over payload — the frame layout
// production writes — with codec as the writer's identity tag:
// [version u8 = 2][uvarint codecLen][codec][uvarint typeLen][type][uvarint
// payloadLen][payload]. It is built by hand rather than through
// cas.EncodeEnvelope so a test's fixture does not move with the writer it is
// meant to pin.
func V2Envelope(codec, typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(2) // envelopeVersion
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(codec)))
	buf.Write(lenBuf[:n])
	buf.WriteString(codec)
	n = binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
}
