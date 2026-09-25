package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// v2Envelope builds a version 2 TLV envelope — the layout this build writes, with
// the codec identity tag in front of the type name — for the viewer tests.
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

// seedHeaderCensus renders one store holding the three header shapes a census
// must tell apart: a current-format frame with a codec tag, a version 1 frame
// whose codec is unspecified, and raw bytes with no envelope at all.
func seedHeaderCensus(t *testing.T) (ts *httptest.Server, admin *http.Client, tagged, v1, raw cas.Digest) {
	t.Helper()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(frame []byte) cas.Digest {
		t.Helper()
		d := sha256.Of(frame)
		if err := backend.Put(ctx, d, bytes.NewReader(frame)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	tagged = put(v2Envelope("json", "blob@1", []byte("tagged")))
	v1 = put(tlvEnvelope("note@1", []byte("v1")))
	raw = put([]byte("not an envelope"))
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts = httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, login(t, ts, testStartupToken), tagged, v1, raw
}

// TestHeaderCensusInTheViewer pins the viewer's half of the census: the table
// carries a version and a codec column, a version 1 frame reads as explicitly
// unspecified rather than blank, and raw bytes show the viewer's not-read marker
// instead of an invented version (viewer-design §3).
func TestHeaderCensusInTheViewer(t *testing.T) {
	ts, admin, _, _, _ := seedHeaderCensus(t)
	page := getBody(t, admin, ts.URL+"/viewer/objects")
	for _, want := range []string{
		"Envelope version", "Codec",
		">2<", ">json<", // the current-format frame
		">1<", ">unspecified<", // the version 1 frame
		">—<", // the headerless object
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("object browser is missing %s: %.800q", want, page)
		}
	}
	// The inspector shows the same two fields for the selected object.
	if !strings.Contains(page, "<dt>Envelope version</dt>") || !strings.Contains(page, "<dt>Codec</dt>") {
		t.Fatalf("inspector has no version/codec row: %.800q", page)
	}
}

// TestHeaderCensusFiltersAreExact pins the two new axes: each lists exactly the
// objects whose cells carry the value, `codec=unspecified` selects the version 1
// frame and not the headerless object, and a value the store does not hold is a
// malformed query at 400 rather than an empty page.
func TestHeaderCensusFiltersAreExact(t *testing.T) {
	ts, admin, tagged, v1, raw := seedHeaderCensus(t)
	for _, tc := range []struct {
		query   string
		want    cas.Digest
		notWant []cas.Digest
	}{
		{"/viewer/objects?version=2", tagged, []cas.Digest{v1, raw}},
		{"/viewer/objects?version=1", v1, []cas.Digest{tagged, raw}},
		{"/viewer/objects?codec=json", tagged, []cas.Digest{v1, raw}},
		{"/viewer/objects?codec=unspecified", v1, []cas.Digest{tagged, raw}},
		{"/viewer/objects?version=2&codec=json", tagged, []cas.Digest{v1, raw}},
	} {
		page := getBody(t, admin, ts.URL+tc.query)
		if !strings.Contains(page, shortDigest(tc.want)) {
			t.Fatalf("%s did not list %s: %.600q", tc.query, tc.want, page)
		}
		for _, digest := range tc.notWant {
			if strings.Contains(page, shortDigest(digest)) {
				t.Fatalf("%s listed %s, which does not match", tc.query, digest)
			}
		}
	}
	for _, query := range []string{"/viewer/objects?codec=nope", "/viewer/objects?version=9", "/viewer/objects?version=x"} {
		if got := statusCode(t, admin, ts.URL+query); got != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", query, got)
		}
	}
}

// TestCodecDifferenceIsNotCorruption pins the axis the census exists to keep
// honest: an object written with a codec tag whose bytes still hash to their
// address verifies, so the integrity axis stays address-based while the codec
// cell names the format (viewer-design §3, cas-core §4.8).
func TestCodecDifferenceIsNotCorruption(t *testing.T) {
	ts, admin, tagged, _, _ := seedHeaderCensus(t)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))
	resp, err := admin.PostForm(ts.URL+"/viewer/objects/"+tagged.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(string(body), "Corrupt") {
		t.Fatalf("an intact frame carrying a codec tag was reported corrupt: %.400q", body)
	}
	page := getBody(t, admin, ts.URL+"/viewer/objects?codec=json")
	if !strings.Contains(page, ">json<") {
		t.Fatalf("the codec cell no longer names the format: %.400q", page)
	}
}
