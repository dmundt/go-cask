// Tests for the object browser's query state: filtering, ordering, paging,
// selection, and the trail that follows references.

package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
)

func TestViewerUsesInjectedHasherForRoutes(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := tlvEnvelope("blob@1", []byte("sha512 viewer"))
	digest := sha512.Of(payload)
	if err := raw.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken, Hasher: sha512.New(), HashAlgorithm: sha512.Name})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	admin := login(t, ts, testStartupToken)
	resp, err := admin.Get(ts.URL + "/viewer/objects/" + digest.String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), digest.String()) {
		t.Fatalf("SHA-512 permalink = %d, want 200 containing %s", resp.StatusCode, digest)
	}
	metadata := getBody(t, admin, ts.URL+"/viewer/objects?selected="+digest.String()+"&tab=metadata")
	if !strings.Contains(metadata, "sha512") {
		t.Fatalf("SHA-512 metadata is missing selected algorithm (contains Algorithm=%v): %.500s", strings.Contains(metadata, "Algorithm"), metadata)
	}
	csrf := csrfFromPage(string(body))
	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+digest.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SHA-512 verify = %d, want 200", resp.StatusCode)
	}
}

func TestFilterDroppingSelectionSwapsInspector(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blob := tlvEnvelope("blob@1", []byte("blob"))
	tree := tlvEnvelope("tree@1", []byte("tree"))
	blobDigest, treeDigest := sha256.Of(blob), sha256.Of(tree)
	for payload, digest := range map[string]cas.Digest{string(blob): blobDigest, string(tree): treeDigest} {
		if err := raw.Put(ctx, digest, strings.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The blob is selected, then a type filter drops it. htmx swaps the list
	// only, so the fallback selection reaches the screen just in case the
	// response carries the inspector out of band.
	target := ts.URL + "/viewer/objects?selected=" + url.QueryEscape(blobDigest.String()) + "&type=tree%401"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "object-list")
	resp, err := viewer.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filtered list = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, `id="object-inspector" hx-swap-oob="true"`) {
		t.Fatalf("list swap must carry the inspector out of band: %.900q", page)
	}
	if !strings.Contains(page, `value="`+treeDigest.String()+`"`) {
		t.Fatalf("inspector must show the surviving row %s: %.900q", treeDigest, page)
	}
	if strings.Contains(page, blobDigest.String()) {
		t.Fatalf("filtered-out object must not linger in the inspector: %.900q", page)
	}
}

func TestOrphanedObjectStateRequiresReachability(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reachable := sha256.Of([]byte("reachable"))
	orphaned := sha256.Of([]byte("orphaned"))
	for _, digest := range []cas.Digest{reachable, orphaned} {
		if err := raw.Put(ctx, digest, bytes.NewReader([]byte(digest.String()))); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{reachable.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	resp, err := viewer.Get(ts.URL + "/viewer/objects?reach=orphaned")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), shortDigest(orphaned)) ||
		strings.Contains(string(body), shortDigest(reachable)) || !strings.Contains(string(body), `value="orphaned" selected`) {
		t.Fatalf("orphaned filter = (%d, %.400q)", resp.StatusCode, body)
	}

	unconfigured, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	unconfiguredServer := httptest.NewTLSServer(unconfigured.Handler())
	t.Cleanup(unconfiguredServer.Close)
	unconfiguredViewer := login(t, unconfiguredServer, testStartupToken)
	resp, err = unconfiguredViewer.Get(unconfiguredServer.URL + "/viewer/objects?reach=orphaned")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfigured orphaned filter = %d, want 400", resp.StatusCode)
	}

	// Integrity is one axis with exclusive states.
	corrupt := getBody(t, viewer, ts.URL+"/viewer/objects?status=corrupt")
	if strings.Contains(corrupt, shortDigest(orphaned)) || strings.Contains(corrupt, shortDigest(reachable)) {
		t.Fatalf("unverified objects must not match status=corrupt: %.800q", corrupt)
	}
	// Reachability is the other axis, so it narrows the integrity match.
	narrowed := getBody(t, viewer, ts.URL+"/viewer/objects?status=not-verified&reach=orphaned")
	if !strings.Contains(narrowed, shortDigest(orphaned)) || strings.Contains(narrowed, shortDigest(reachable)) {
		t.Fatalf("reachability filter must narrow the integrity match: %.800q", narrowed)
	}
	// Reachability is no longer an integrity state.
	if code := statusCode(t, viewer, ts.URL+"/viewer/objects?status=orphaned"); code != http.StatusBadRequest {
		t.Fatalf("orphaned as an integrity state = %d, want 400", code)
	}
	if code := statusCode(t, viewer, ts.URL+"/viewer/objects?reach=bogus"); code != http.StatusBadRequest {
		t.Fatalf("unknown reachability = %d, want 400", code)
	}
}

func TestDetachedStateRequiresOrphanhoodAndNoInboundReferences(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolved := sha256.Of([]byte("resolved"))
	orphaned := sha256.Of([]byte("orphaned-with-inbound"))
	detached := sha256.Of([]byte("detached"))
	for _, digest := range []cas.Digest{resolved, orphaned, detached} {
		if err := raw.Put(ctx, digest, bytes.NewReader([]byte(digest.String()))); err != nil {
			t.Fatal(err)
		}
	}
	references := newTestReferenceIndex()
	references.Record(resolved, []cas.Digest{orphaned})
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		References:   references,
		Reachability: testReachabilityIndex{resolved.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	page := getBody(t, viewer, ts.URL+"/viewer/objects?reach=detached")
	if !strings.Contains(page, shortDigest(detached)) ||
		strings.Contains(page, shortDigest(orphaned)) ||
		strings.Contains(page, shortDigest(resolved)) ||
		!strings.Contains(page, `viewer-status-detached">Detached`) {
		t.Fatalf("detached filter = %.900q", page)
	}

	page = getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(detached.String()))
	if !strings.Contains(page, `viewer-status-detached">Detached`) {
		t.Fatalf("detached inspector state = %.900q", page)
	}

	withoutReferences, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{resolved.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutReferencesServer := httptest.NewTLSServer(withoutReferences.Handler())
	t.Cleanup(withoutReferencesServer.Close)
	withoutReferencesViewer := login(t, withoutReferencesServer, testStartupToken)
	if code := statusCode(t, withoutReferencesViewer, withoutReferencesServer.URL+"/viewer/objects?reach=detached"); code != http.StatusBadRequest {
		t.Fatalf("detached filter without references = %d, want 400", code)
	}
}

func TestRootStateRequiresReachabilityAndNoInboundReferences(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := sha256.Of([]byte("root"))
	interior := sha256.Of([]byte("interior"))
	orphaned := sha256.Of([]byte("orphaned"))
	for _, digest := range []cas.Digest{root, interior, orphaned} {
		if err := raw.Put(ctx, digest, bytes.NewReader([]byte(digest.String()))); err != nil {
			t.Fatal(err)
		}
	}
	references := newTestReferenceIndex()
	// root has no inbound edges recorded, so it can only be reachable as an
	// entry point; interior gets an inbound edge from root, so it is
	// reachable but not a Root.
	references.Record(root, []cas.Digest{interior})
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		References:   references,
		Reachability: testReachabilityIndex{root.String(): true, interior.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	page := getBody(t, viewer, ts.URL+"/viewer/objects?reach=root")
	if !strings.Contains(page, shortDigest(root)) ||
		strings.Contains(page, shortDigest(interior)) ||
		strings.Contains(page, shortDigest(orphaned)) ||
		!strings.Contains(page, `viewer-status-root">Root`) {
		t.Fatalf("root filter = %.900q", page)
	}

	page = getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(root.String()))
	if !strings.Contains(page, `viewer-status-root">Root`) {
		t.Fatalf("root inspector state = %.900q", page)
	}

	withoutReferences, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{root.String(): true, interior.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutReferencesServer := httptest.NewTLSServer(withoutReferences.Handler())
	t.Cleanup(withoutReferencesServer.Close)
	withoutReferencesViewer := login(t, withoutReferencesServer, testStartupToken)
	if code := statusCode(t, withoutReferencesViewer, withoutReferencesServer.URL+"/viewer/objects?reach=root"); code != http.StatusBadRequest {
		t.Fatalf("root filter without references = %d, want 400", code)
	}
}

// TestAllFourReferenceStatesAreMutuallyExclusive seeds one object for each of
// the four reachable/inbound combinations in a single store and asserts that
// every reach= filter selects exactly its own object, no filter leaks another
// state's object, and the table pill, dropdown option, and inspector state
// agree for every state.
func TestAllFourReferenceStatesAreMutuallyExclusive(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := sha256.Of([]byte("all-states-root"))                      // reachable, inbound 0
	resolved := sha256.Of([]byte("all-states-resolved"))              // reachable, inbound > 0
	orphaned := sha256.Of([]byte("all-states-orphaned-with-inbound")) // unreachable, inbound > 0
	detached := sha256.Of([]byte("all-states-detached"))              // unreachable, inbound 0
	for _, digest := range []cas.Digest{root, resolved, orphaned, detached} {
		if err := raw.Put(ctx, digest, bytes.NewReader([]byte(digest.String()))); err != nil {
			t.Fatal(err)
		}
	}
	references := newTestReferenceIndex()
	references.Record(root, []cas.Digest{resolved})
	references.Record(resolved, []cas.Digest{orphaned})
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		References:   references,
		Reachability: testReachabilityIndex{root.String(): true, resolved.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	for _, test := range []struct {
		name   string
		digest cas.Digest
		pill   string
	}{
		{"reachable", root, `viewer-status-root">Root`},
		{"reachable", resolved, `viewer-status-reachable">Resolved`},
		{"orphaned", orphaned, `viewer-status-orphaned">Orphaned`},
		{"detached", detached, `viewer-status-detached">Detached`},
	} {
		page := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(test.digest.String()))
		if !strings.Contains(page, test.pill) {
			t.Fatalf("inspector state for %s = %.900q, want pill %q", test.digest, page, test.pill)
		}
	}

	// reach= expected membership per digest. "orphaned" matches every
	// unreachable row, including the Detached one (Detached is a stricter
	// subset of Orphaned: it just wins pill priority); "reachable" matches
	// every reachable row, including Root.
	membership := map[string]map[string]bool{
		"reachable": {root.String(): true, resolved.String(): true},
		"root":      {root.String(): true},
		"orphaned":  {orphaned.String(): true, detached.String(): true},
		"detached":  {detached.String(): true},
	}
	for reach, want := range membership {
		page := getBody(t, viewer, ts.URL+"/viewer/objects?reach="+reach)
		for _, other := range []cas.Digest{root, resolved, orphaned, detached} {
			shouldMatch := want[other.String()]
			contains := strings.Contains(page, shortDigest(other))
			if shouldMatch && !contains {
				t.Fatalf("reach=%s missing %s: %.900q", reach, other, page)
			}
			if !shouldMatch && contains {
				t.Fatalf("reach=%s unexpectedly matched %s: %.900q", reach, other, page)
			}
		}
	}

	// The dropdown offers every state exactly once, in Resolved/Orphaned/
	// Root/Detached order, and marks the active selection.
	dropdown := getBody(t, viewer, ts.URL+"/viewer/objects?reach=detached")
	for _, option := range []string{
		`value="reachable"`,
		`value="orphaned"`,
		`value="root"`,
		`value="detached" selected`,
	} {
		if !strings.Contains(dropdown, option) {
			t.Fatalf("reach dropdown missing %q: %.900q", option, dropdown)
		}
	}
}

func TestEmptyObjectListKeepsInspectorEmpty(t *testing.T) {
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	page := getBody(t, viewer, ts.URL+"/viewer/objects")
	if !strings.Contains(page, "Select an object to inspect it.") {
		t.Fatalf("an empty store has nothing to auto-select: %.400q", page)
	}
	if strings.Contains(page, `class="viewer-selected"`) {
		t.Fatalf("an empty store must not mark a selected row: %.400q", page)
	}
}

func TestInspectorTrailStepsThroughVisitedObjects(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := make([]string, 0, 3)
	for _, word := range []string{"alpha", "beta", "gamma"} {
		payload := []byte(word)
		h := sha256.Of(payload)
		if err := raw.Put(ctx, h, bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
		digests = append(digests, h.String())
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// A single visit has nowhere to step, so both controls render inert.
	first := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[0]))
	if !strings.Contains(first, `<span class="viewer-history-step" aria-disabled="true"`) {
		t.Fatalf("a fresh trail must disable both steps: %.800q", first)
	}
	if strings.Contains(first, `<a class="viewer-history-step"`) {
		t.Fatalf("a fresh trail must offer no step link: %.800q", first)
	}

	getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[1])+"&nav=ref")
	second := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[2])+"&nav=ref")
	if !strings.Contains(second, "selected="+url.QueryEscape(digests[1])+"&amp;nav=trail") {
		t.Fatalf("the previous step must point at the previously visited object: %.800q", second)
	}

	// Stepping back exposes the forward step without extending the trail.
	back := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[1])+"&nav=trail")
	if !strings.Contains(back, "selected="+url.QueryEscape(digests[0])+"&amp;nav=trail") {
		t.Fatalf("stepping back must keep walking backwards: %.800q", back)
	}
	if !strings.Contains(back, "selected="+url.QueryEscape(digests[2])+"&amp;nav=trail") {
		t.Fatalf("stepping back must expose the forward step: %.800q", back)
	}

	// Picking a row in the table is a new point of departure, so the chain the
	// operator just abandoned must not remain reachable.
	picked := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[2]))
	if strings.Contains(picked, `<a class="viewer-history-step"`) {
		t.Fatalf("selecting a row must reset the trail: %.800q", picked)
	}
}

func TestClickingSelectedRowEmptiesInspector(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("toggle")
	if err := raw.Put(ctx, sha256.Of(payload), bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The auto-selected row links at its own deselected state, and following
	// that link must leave the inspector empty instead of re-selecting it.
	page := getBody(t, viewer, ts.URL+"/viewer/objects")
	if !strings.Contains(page, `href="/viewer/objects?selected="`) {
		t.Fatalf("the selected row must link at the deselected state: %.800q", page)
	}
	cleared := getBody(t, viewer, ts.URL+"/viewer/objects?selected=")
	if !strings.Contains(cleared, "Select an object to inspect it.") {
		t.Fatalf("an explicit deselect must empty the inspector: %.800q", cleared)
	}
	if strings.Contains(cleared, `class="viewer-selected"`) {
		t.Fatalf("an explicit deselect must leave no row marked: %.800q", cleared)
	}
}

func TestFilteredOutSelectionFallsBackToFirstRow(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := sha256.Of([]byte("kept"))
	if err := raw.Put(ctx, kept, bytes.NewReader([]byte("kept"))); err != nil {
		t.Fatal(err)
	}
	dropped := sha256.Of([]byte("dropped"))
	if err := raw.Put(ctx, dropped, bytes.NewReader(tlvEnvelope("blob@1", []byte("dropped")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The type filter excludes the selected object, so the inspector must fall
	// back to a visible row instead of rendering nothing.
	page := getBody(t, viewer, ts.URL+"/viewer/objects?type=blob@1&selected="+url.QueryEscape(kept.String()))
	if strings.Contains(page, "Select an object to inspect it.") {
		t.Fatalf("a filtered-out selection must fall back to the first row: %.800q", page)
	}
	if !strings.Contains(page, `class="viewer-selected" aria-current="true"`) {
		t.Fatalf("the fallback row must render as selected: %.800q", page)
	}
}

func TestIntegrityAndReachabilityAreIndependentColumns(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Stored bytes deliberately do not hash to their address, so Verify fails.
	orphaned := sha256.Of([]byte("orphaned-corrupt"))
	if err := raw.Put(ctx, orphaned, bytes.NewReader(tlvEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	resp, err := admin.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(orphaned.String()) + "&tab=metadata")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "viewer-status-orphaned") {
		t.Fatalf("unverified orphan must render as orphaned: %.400q", page)
	}
	if !strings.Contains(string(page), "viewer-status-not-verified") {
		t.Fatalf("orphan status column must report integrity, not reachability: %.400q", page)
	}
	// Verify must stay available for orphans: they are the likeliest to rot.
	if !strings.Contains(string(page), "/verify") {
		t.Fatalf("orphan actions panel must still offer Verify: %.400q", page)
	}
	csrf := csrfFromPage(string(page))
	if csrf == "" {
		t.Fatalf("actions page has no CSRF token: %.400q", page)
	}

	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+orphaned.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "corrupt") {
		t.Fatalf("verify = (%d, %.200q), want corrupt result", resp.StatusCode, body)
	}

	resp, err = admin.Get(ts.URL + "/viewer/objects")
	if err != nil {
		t.Fatal(err)
	}
	page, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "viewer-status-corrupt") {
		t.Fatalf("integrity column must report the corrupt integrity result: %.400q", page)
	}
	// The two axes are independent columns, so the orphan pill survives a
	// corrupt integrity result instead of being hidden behind it.
	if !strings.Contains(string(page), "viewer-status-orphaned") {
		t.Fatalf("a corrupt orphan must still show its orphan pill: %.400q", page)
	}
	// Each axis owns a column of its own: "Status" used to carry both, which
	// made Orphaned read as an integrity verdict.
	for _, want := range []string{">Integrity<", ">References<"} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("object table is missing the %s column header: %.900q", want, page)
		}
	}
	if strings.Contains(string(page), "viewer-status-extra") {
		t.Fatalf("the two axes must not share one cell any more: %.400q", page)
	}
	// The non-orphan verdict reads as "Resolved": "Reachable" invited the
	// reading that an object with no inbound references cannot be reachable,
	// when a root has none by definition.
	if !strings.Contains(string(page), ">Resolved<") || strings.Contains(string(page), ">Reachable<") {
		t.Fatalf("reference column must label the sound state Resolved: %.900q", page)
	}
	// The reference column sorts like every other data column.
	if !strings.Contains(string(page), "sort=reach") {
		t.Fatalf("reference column header must offer a sort link: %.1500q", page)
	}

	// Both facts stay independently filterable, each on its own axis.
	for _, query := range []string{"status=corrupt", "reach=orphaned"} {
		resp, err = admin.Get(ts.URL + "/viewer/objects?" + query)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), shortDigest(orphaned)) {
			t.Fatalf("%s must match the corrupt orphan: (%d, %.300q)", query, resp.StatusCode, body)
		}
	}
}

// Switching tabs only swaps the inspector, so the table's row links would keep
// the tab they were rendered with and silently throw the operator back to
// Metadata on the next pick. Tab links therefore re-render the list too.
func TestTabSelectionSurvivesPickingAnotherRow(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := make([]cas.Digest, 0, 2)
	for _, payload := range [][]byte{[]byte("first"), []byte("second")} {
		data := tlvEnvelope("blob@1", payload)
		digest := sha256.Of(data)
		if err := raw.Put(ctx, digest, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	selected := digests[0]
	page := getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(selected.String())+"&tab=bytes")

	// A tab click must not be mistaken for a fresh selection: it has to leave
	// the visit trail exactly where it was.
	if !strings.Contains(page, "nav="+navStay) {
		t.Fatalf("inspector tab links must carry the %q marker: %.900q", navStay, page)
	}
	// ... and it has to swap the object list out of band, which the viewer
	// only does when the request asks for the selection fragment.
	tabLink := `hx-get="/viewer/objects?selected=` + selected.String() + `&amp;tab=references&amp;nav=` + navStay +
		`" hx-target="#object-inspector" hx-headers='{"X-Viewer-Selection":"true"}'`
	if !strings.Contains(page, tabLink) {
		t.Fatalf("tab link must also refresh the object list: %.2000q", page)
	}

	// The row links rendered while Bytes is open must keep the operator there.
	other := digests[1].String()
	rowLink := `href="/viewer/objects?selected=` + other + `&amp;tab=bytes"`
	if !strings.Contains(page, rowLink) {
		t.Fatalf("row links dropped the open tab, want %s: %.2000q", rowLink, page)
	}

	// Follow the tab link the way htmx does. The marker has to survive state
	// parsing, and the response has to carry the re-rendered list.
	req, err := http.NewRequest(http.MethodGet,
		ts.URL+"/viewer/objects?selected="+url.QueryEscape(selected.String())+"&tab=references&nav="+navStay, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "object-inspector")
	req.Header.Set("X-Viewer-Selection", "true")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tab switch rejected: status=%d body=%.300q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `id="object-list"`) {
		t.Fatalf("tab switch must swap the object list out of band: %.900q", body)
	}
	if !strings.Contains(string(body), `href="/viewer/objects?selected=`+other+`&amp;tab=references"`) {
		t.Fatalf("re-rendered row links did not adopt the new tab: %.2000q", body)
	}
}

func TestObjectBrowserQueryState(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		typeName := "blob@1"
		if i == 0 {
			typeName = "note@1"
		}
		data := tlvEnvelope(typeName, bytes.Repeat([]byte{byte(i)}, i+1))
		digest := sha256.Of(data)
		if err := srv.store.Put(ctx, digest, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	viewer := login(t, ts, "viewer-tok")

	t.Run("filter and first page", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?type=blob%401&limit=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("filtered list = %d, want 200", resp.StatusCode)
		}
		page := string(body)
		if !strings.Contains(page, "1–25 of 29") {
			t.Fatalf("first page summary missing: %.400q", page)
		}
		if !strings.Contains(page, "type=blob%401") || !strings.Contains(page, "offset=25") {
			t.Fatalf("next pager did not retain query state: %.400q", page)
		}
	})

	t.Run("last page", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?type=blob%401&limit=25&offset=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "26–29 of 29") {
			t.Fatalf("last page = (%d, %.400q), want final summary", resp.StatusCode, body)
		}
	})

	t.Run("empty and out of range pages", func(t *testing.T) {
		for _, test := range []struct {
			query string
			want  string
		}{
			{query: "q=missing", want: "0–0 of 0"},
			{query: "type=blob%401&limit=25&offset=100", want: "0–0 of 29"},
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + test.query)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), test.want) {
				t.Fatalf("page %q = (%d, %.400q), want %q", test.query, resp.StatusCode, body, test.want)
			}
		}
	})

	t.Run("sort and filter controls", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?sort=size&dir=desc&offset=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			`aria-sort="descending"`,
			`name="sort" type="hidden" value="size"`,
			`name="dir" type="hidden" value="desc"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("sort controls missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
		if strings.Contains(page, `name="offset"`) {
			t.Fatalf("filter form must reset pagination: %.400q", page)
		}
	})

	t.Run("invalid query", func(t *testing.T) {
		for _, rawQuery := range []string{
			"limit=ten",
			"offset=-1",
			"sort=references",
			"dir=sideways",
			"size=huge",
			"status=orphaned",
			"selected=not-a-digest",
			"tab=unknown",
			"type=missing%401",
			"q=one&q=two",
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("query %q = %d, want 400", rawQuery, resp.StatusCode)
			}
		}
	})

	// Every data column sorts: a column that shows a fact the operator cannot
	// order by is a dead end. The reference column is asserted where a
	// reachability index exists, since it is absent without one.
	t.Run("sortable columns", func(t *testing.T) {
		page := getBody(t, viewer, ts.URL+"/viewer/objects")
		for _, want := range []string{"sort=inbound", "sort=status", "sort=size"} {
			if !strings.Contains(page, want) {
				t.Fatalf("object table header is missing %q: %.1500q", want, page)
			}
		}
		sortedPage := getBody(t, viewer, ts.URL+"/viewer/objects?sort=inbound&dir=desc")
		if !strings.Contains(sortedPage, `aria-sort="descending"`) {
			t.Fatalf("sort=inbound did not mark the sorted column: %.1500q", sortedPage)
		}
	})

	// A page size is a preference, not a claim about the store, so a size the
	// control does not offer is honoured within bounds and clamped outside
	// them. Rejecting it made a hand-edited or stale URL fail outright.
	t.Run("page size outside the offered set", func(t *testing.T) {
		for _, test := range []struct{ query, want string }{
			{query: "limit=10", want: `value="10" selected`},
			{query: "limit=0", want: `value="1" selected`},
			{query: "limit=100000", want: `value="250" selected`},
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + test.query)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("query %q = %d, want 200", test.query, resp.StatusCode)
			}
			// The control must name the size actually in effect, or it would
			// silently read as one of the presets.
			if !strings.Contains(string(body), test.want) {
				t.Fatalf("query %q: rows control missing %q: %.900q", test.query, test.want, body)
			}
		}
	})
}

func TestSortObjectRows(t *testing.T) {
	rows := []objectRow{
		{Digest: "b", Type: "tree@1", Size: 12, References: 1, Orphaned: true, Integrity: "corrupt"},
		{Digest: "a", Type: "blob@1", Size: 4, References: 7, Integrity: "not-verified"},
		{Digest: "c", Type: "blob@1", Size: 8, References: 3, Orphaned: true, Integrity: "verified"},
	}
	for _, test := range []struct {
		state objectBrowserState
		want  string
	}{
		{state: objectBrowserState{Sort: "hash", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "type", Direction: "desc"}, want: "bca"},
		{state: objectBrowserState{Sort: "size", Direction: "asc"}, want: "acb"},
		// Integrity is ranked, not compared as text: ascending reads verified,
		// unverified, corrupt — sound first, like every other axis. Sorting the
		// keys alphabetically would lead with the corrupt object.
		{state: objectBrowserState{Sort: "status", Direction: "asc"}, want: "cab"},
		{state: objectBrowserState{Sort: "status", Direction: "desc"}, want: "bac"},
		{state: objectBrowserState{Sort: "inbound", Direction: "asc"}, want: "bca"},
		{state: objectBrowserState{Sort: "inbound", Direction: "desc"}, want: "acb"},
		// Ascending lists the sound state first, like the other axes; the
		// digest breaks the tie within each group.
		{state: objectBrowserState{Sort: "reach", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "reach", Direction: "desc"}, want: "cba"},
	} {
		sorted := append([]objectRow(nil), rows...)
		sortObjectRows(sorted, test.state)
		var got strings.Builder
		for _, row := range sorted {
			got.WriteString(row.Digest)
		}
		if got.String() != test.want {
			t.Fatalf("sort %+v = %q, want %q", test.state, got.String(), test.want)
		}
	}
}

// TestSortHeaderLabelsFollowTheActiveColumn pins the accessible name of every
// sort control. An inactive column always sorts ascending on the next click,
// so only the active column may announce a flip — a header that reads the
// direction without first checking it owns the sort tells a screen reader the
// opposite of what clicking it does.
func TestSortHeaderLabelsFollowTheActiveColumn(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	data := tlvEnvelope("blob@1", []byte("sort-header"))
	if err := srv.store.Put(ctx, sha256.Of(data), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	viewer := login(t, ts, "viewer-tok")

	// The reachability column is absent without a Reachability index, which
	// this server has none of, so the six always-present columns are pinned.
	columns := []string{"hash", "type", "size", "inbound references", "integrity", "written"}
	for _, sorted := range []string{"hash", "size", "written"} {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?sort=" + sorted + "&dir=asc")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, column := range columns {
			want := "ascending"
			if column == sorted {
				want = "descending"
			}
			label := `aria-label="Sort ` + column + " " + want + `"`
			if !strings.Contains(page, label) {
				t.Errorf("sort=%s: missing %s", sorted, label)
			}
		}
	}
}
