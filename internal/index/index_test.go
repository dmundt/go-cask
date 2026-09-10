package index

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestPaginate(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5}
	cases := []struct {
		offset, limit int
		want          []int
	}{
		{0, 100, items},
		{0, 2, []int{0, 1}},
		{2, 2, []int{2, 3}},
		{10, 2, []int{}},     // offset beyond end
		{-1, 2, []int{0, 1}}, // negative offset clamped
		{4, 100, []int{4, 5}},
	}
	for _, tc := range cases {
		got := Paginate(items, tc.offset, tc.limit)
		if len(got) != len(tc.want) {
			t.Fatalf("Paginate(%d,%d) = %v, want %v", tc.offset, tc.limit, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Paginate(%d,%d) = %v, want %v", tc.offset, tc.limit, got, tc.want)
			}
		}
	}
}

// tlvEnvelope builds a TLV envelope ([version][uvarint typeLen][type][uvarint
// payloadLen][payload], cas-core §8 decision 1) for the test input.
func tlvEnvelope(typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // envelopeVersion
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
}

// TestEnvelopeType pins the best-effort envelope sniffing contract against
// the TLV envelope: the versioned type name is returned when the bytes are a
// TLV envelope; "" otherwise (raw objects, or any non-TLV bytes, have no
// type). A legacy unversioned type name reads back as "@1".
func TestEnvelopeType(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"versioned type", tlvEnvelope("blob@1", []byte("x")), "blob@1"},
		{"legacy unversioned type reads as @1", tlvEnvelope("blob", []byte("x")), "blob@1"},
		{"other versioned type", tlvEnvelope("commit@1", []byte("{}")), "commit@1"},
		{"empty payload is still typed", tlvEnvelope("blob@1", nil), "blob@1"},
		{"garbage bytes are not an envelope", []byte("not an envelope"), ""},
		{"JSON object is not a TLV envelope", []byte(`{"type":"blob@1","data":"aGk="}`), ""},
		{"empty input", nil, ""},
		{"type length beyond buffer", []byte{1, 200, 'a'}, ""},
		{"truncated payload still yields the type", tlvEnvelope("blob@1", bytes.Repeat([]byte("x"), 1<<20))[:32], "blob@1"},
		{"non-TLV first byte", []byte{2, 1, 'a'}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnvelopeType(tc.in); got != tc.want {
				t.Fatalf("EnvelopeType(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
