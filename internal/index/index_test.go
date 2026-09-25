package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
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

// v2Envelope builds a version 2 TLV envelope — the layout this build writes,
// with the codec identity tag in front of the type name — for the test input.
func v2Envelope(codec, typeName string, payload []byte) []byte {
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

// countingReadCloser counts the bytes a header read pulls through it.
type countingReadCloser struct {
	r io.Reader
	n int
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func (c *countingReadCloser) Close() error { return nil }

// countingSource serves one object through a counting reader, so a test can
// assert what a header read costs.
type countingSource struct {
	*backmem.Backend
	last *countingReadCloser
}

func (s *countingSource) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	rc, err := s.Backend.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	s.last = &countingReadCloser{r: rc}
	return s.last, nil
}

// TestHeaderReportsEveryField pins the census read: one call returns the frame
// version, the codec tag and the versioned type name, whatever the layout — a
// version 2 frame with a tag, one without, and a version 1 frame (whose codec is
// explicitly unspecified).
func TestHeaderReportsEveryField(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	put := func(data []byte) cas.Digest {
		t.Helper()
		d := sha256.Of(data)
		if err := backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	tagged := put(v2Envelope("gzip+json", "blob@1", []byte("x")))
	untagged := put(v2Envelope("", "note@1", []byte("x")))
	v1 := put(tlvEnvelope("legacy", []byte("x")))
	raw := put([]byte("not an envelope"))
	truncated := put([]byte{2, 200, 'a'}) // declared codec length beyond the buffer

	for _, tc := range []struct {
		name    string
		digest  cas.Digest
		version byte
		codec   string
		typ     string
	}{
		{"tagged v2", tagged, 2, "gzip+json", "blob@1"},
		{"untagged v2", untagged, 2, "", "note@1"},
		{"v1", v1, 1, "", "legacy@1"},
		{"raw bytes", raw, 0, "", ""},
		{"truncated", truncated, 0, "", ""},
	} {
		version, codec, typ, err := Header(ctx, backend, tc.digest)
		if err != nil {
			t.Fatalf("%s: Header = %v", tc.name, err)
		}
		if version != tc.version || codec != tc.codec || typ != tc.typ {
			t.Fatalf("%s: Header = (%d, %q, %q), want (%d, %q, %q)",
				tc.name, version, codec, typ, tc.version, tc.codec, tc.typ)
		}
	}
	missing := sha256.Of([]byte("never stored"))
	if _, _, _, err := Header(ctx, backend, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Header(missing) = %v, want cas.ErrNotFound", err)
	}
	failing := &snapshotSource{Backend: backend, getErr: errors.New("boom")}
	if _, _, _, err := Header(ctx, failing, tagged); err == nil {
		t.Fatal("Header with a failing Get must return an error")
	}
}

// TestHeaderReadsOnlyTheHeader pins the cost the census depends on: the bytes
// consumed are the header — version, codec tag and type — independent of the
// payload size, whatever a CLI listing or a viewer snapshot walks.
func TestHeaderReadsOnlyTheHeader(t *testing.T) {
	const codec, typ = "gzip+json", "blob@1"
	ctx := context.Background()
	source := &countingSource{Backend: backmem.New()}
	// The header is everything before the payload-length field; an empty payload
	// frames as exactly one more byte (uvarint 0).
	want := len(v2Envelope(codec, typ, nil)) - 1
	for _, size := range []int{0, 7, 1 << 20} {
		frame := v2Envelope(codec, typ, bytes.Repeat([]byte("x"), size))
		d := sha256.Of(frame)
		if err := source.Put(ctx, d, bytes.NewReader(frame)); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := Header(ctx, source, d); err != nil {
			t.Fatalf("payload %d: Header = %v", size, err)
		}
		if source.last.n != want {
			t.Fatalf("payload %d: read %d bytes, want exactly the %d-byte header", size, source.last.n, want)
		}
	}
}

// TestBuildSnapshotCensusLists pins the per-version and per-codec census: each
// list holds what the store actually contains, an object whose header cannot be
// read contributes to none of them, and "" is listed when some frame carries no
// codec identity.
func TestBuildSnapshotCensusLists(t *testing.T) {
	ctx := context.Background()
	backend := &snapshotSource{Backend: backmem.New(), modTime: time.Unix(42, 0)}
	frames := [][]byte{
		v2Envelope("json", "blob@1", []byte("a")),
		v2Envelope("", "note@1", []byte("b")),
		tlvEnvelope("legacy", []byte("c")),
		[]byte("not an envelope"),
	}
	for _, frame := range frames {
		if err := backend.Put(ctx, sha256.Of(frame), bytes.NewReader(frame)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := BuildSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snapshot.Versions, []byte{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("Versions = %v, want %v", got, want)
	}
	if got, want := snapshot.Codecs, []string{"", "json"}; !slices.Equal(got, want) {
		t.Fatalf("Codecs = %v, want %v", got, want)
	}
	for _, entry := range snapshot.Entries {
		if entry.Type == "" && (entry.Version != 0 || entry.Codec != "") {
			t.Fatalf("headerless entry %s reports version %d codec %q", entry.Digest, entry.Version, entry.Codec)
		}
		if entry.Type != "" && entry.Version == 0 {
			t.Fatalf("typed entry %s reports no frame version", entry.Digest)
		}
	}
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
	backend := backmem.New()
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
	*backmem.Backend
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
	backend := &snapshotSource{Backend: backmem.New(), modTime: time.Unix(42, 0)}
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
		{"get", &snapshotSource{Backend: backmem.New(), getErr: errors.New("read failed")}},
		{"size", &snapshotSource{Backend: backmem.New(), sizeErr: errors.New("stat failed")}},
		{"modtime", &snapshotSource{Backend: backmem.New(), modErr: errors.New("time failed")}},
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
	_, err := BuildSnapshot(ctx, &snapshotSource{Backend: backmem.New()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildSnapshot(canceled) = %v, want context.Canceled", err)
	}
}
