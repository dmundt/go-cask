package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	memory "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
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
// TLV envelope; "" otherwise (backend objects, or any non-TLV bytes, have no
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

// TestHeaderType pins the contract the viewer's index, the inspector and the
// cask CLI share: a readable object yields its versioned type name, bytes that
// are not an envelope yield "" with no error (a raw object written by `cask put`
// is untyped, not damaged), and only an unreadable object returns an error.
func TestHeaderType(t *testing.T) {
	ctx := context.Background()
	backend := memory.New()
	put := func(data []byte) cas.Digest {
		t.Helper()
		d := sha256.Of(data)
		if err := backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	typed := put(tlvEnvelope("blob@1", []byte("x")))
	raw := put([]byte("not an envelope"))
	truncated := put([]byte{1, 200, 'a'}) // declared type length beyond the buffer

	if got, err := HeaderType(ctx, backend, typed); err != nil || got != "blob@1" {
		t.Fatalf("HeaderType(typed) = (%q, %v), want (%q, nil)", got, err, "blob@1")
	}
	if got, err := HeaderType(ctx, backend, raw); err != nil || got != "" {
		t.Fatalf("HeaderType(raw) = (%q, %v), want (\"\", nil): a raw object is untyped, not unreadable", got, err)
	}
	if got, err := HeaderType(ctx, backend, truncated); err != nil || got != "" {
		t.Fatalf("HeaderType(truncated) = (%q, %v), want (\"\", nil)", got, err)
	}
	missing := sha256.Of([]byte("never stored"))
	if _, err := HeaderType(ctx, backend, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("HeaderType(missing) = %v, want cas.ErrNotFound", err)
	}
	// A read failure is an error, not a silently empty type.
	failing := &snapshotSource{Backend: backend, getErr: errors.New("boom")}
	if _, err := HeaderType(ctx, failing, typed); err == nil {
		t.Fatal("HeaderType with a failing Get must return an error")
	}
}

type snapshotSource struct {
	*memory.Backend
	getErr  error
	sizeErr error
	modErr  error
	modTime time.Time
}

func (s *snapshotSource) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.Backend.Get(ctx, d)
}

func (s *snapshotSource) Size(context.Context, cas.Digest) (int64, error) {
	if s.sizeErr != nil {
		return 0, s.sizeErr
	}
	return 1, nil
}

func (s *snapshotSource) ModTime(context.Context, cas.Digest) (time.Time, error) {
	if s.modErr != nil {
		return time.Time{}, s.modErr
	}
	return s.modTime, nil
}

func TestBuildSnapshot(t *testing.T) {
	ctx := context.Background()
	backend := &snapshotSource{Backend: memory.New(), modTime: time.Unix(42, 0)}
	typed := tlvEnvelope("blob@1", []byte("payload"))
	untyped := []byte("backend")
	for _, object := range []struct {
		data []byte
	}{
		{typed},
		{untyped},
	} {
		if err := backend.Put(ctx, sha256.Of(object.data), bytes.NewReader(object.data)); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := BuildSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Total != 2 || snapshot.Bytes != 2 {
		t.Fatalf("snapshot totals = (%d, %d), want (2, 2)", snapshot.Total, snapshot.Bytes)
	}
	if len(snapshot.Entries) != 2 || len(snapshot.Types) != 1 || snapshot.Types[0] != "blob@1" {
		t.Fatalf("snapshot = %#v, want two entries and one type", snapshot)
	}
	for _, entry := range snapshot.Entries {
		if entry.Unreadable || !entry.Written.Equal(backend.modTime) {
			t.Fatalf("entry = %#v, want readable entry at %v", entry, backend.modTime)
		}
	}
}

func TestBuildSnapshotRecordsMetadataErrors(t *testing.T) {
	ctx := context.Background()
	digest := sha256.Of([]byte("object"))
	cases := []struct {
		name   string
		source *snapshotSource
	}{
		{"get", &snapshotSource{Backend: memory.New(), getErr: errors.New("read failed")}},
		{"size", &snapshotSource{Backend: memory.New(), sizeErr: errors.New("stat failed")}},
		{"modtime", &snapshotSource{Backend: memory.New(), modErr: errors.New("time failed")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.source.Backend.Put(ctx, digest, bytes.NewReader([]byte("object"))); err != nil {
				t.Fatal(err)
			}
			snapshot, err := BuildSnapshot(ctx, tc.source)
			if err != nil || len(snapshot.Entries) != 1 || !snapshot.Entries[0].Unreadable {
				t.Fatalf("BuildSnapshot() = (%#v, %v), want one unreadable entry", snapshot, err)
			}
		})
	}
}

func TestBuildSnapshotHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildSnapshot(ctx, &snapshotSource{Backend: memory.New()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildSnapshot(canceled) = %v, want context.Canceled", err)
	}
}
