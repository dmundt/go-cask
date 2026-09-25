// Branches the viewer's suite deliberately leaves uncovered, each with the
// reason it is recorded rather than tested (testing-strategy §5: "uncovered
// defensive branches no test can reach deterministically stay in the lower
// tier, and say so").
//
// This file deliberately contains no tests; its only job is to be the written
// record for the uncovered blocks the rest of the package's tests cannot
// reach, so a reviewer can see each one was considered instead of assuming the
// suite forgot it. It carries no build tag: the record is compiled, formatted
// and vetted with the rest of the package, so it cannot rot unnoticed.

package web

// The branches below are unreachable by construction, not untested.

// --- Failure branches production code cannot produce ---

// auth.go: loginToken's error after sessions.create, and randomHex's error
//
//	Both need crypto/rand.Read to fail. The viewer takes its entropy from the
//	OS source and has no injectable entropy seam — deliberately, since a seam
//	there would be a way to weaken session and CSRF generation. A test can only
//	reach these by adding such a seam to production code.
//
// auth.go: sameOrigin's url.Parse failure
//
//	url.Parse accepts nearly anything a header value can carry, so the parse
//	failure exists for input net/http rejects at the request line first.
//
// auth.go: csrfFor's and roleFor's empty returns
//
//	Both are called from handlers registered behind require, which looked the
//	session up at the top of the same request. Nothing between the two lookups
//	can remove it: the sweep runs on create and get is not called again by the
//	handler. The empty answers are unreachable while the middleware order
//	holds.
//
// csrf.go: csrfOK's nil-session exemption
//
//	Its only caller is require, which passes the session it just looked up, so
//	the argument is never nil. The exemption documents the login POST, which
//	does not route through csrfOK at all.
//
// verify.go: verifyAllFragment's canceled-request branch
//
//	The check reads r.Context().Err() between objects. A handler test can only
//	hand the handler a context that is already canceled — which fails the
//	store's List first and answers 500 — so reaching it needs a cancellation
//	timed between two objects in the sweep.
//
// verify.go: verifyObject's close-error branch
//
//	The store's Get returns an *os.File-backed reader whose Close succeeds for
//	every object the viewer can open. srv.store is a concrete *fs.Backend, so
//	there is no seam to inject a reader whose Close fails after a read that
//	succeeded.
//
// web.go: the response-write branches (htmxScript, stylesheet, and render's
// body write)
//
//	These report a failure to write the response body. http.ResponseWriter
//	offers no way to make a write fail deterministically on request: the only
//	production cause is a client that disconnected mid-response, which is a
//	race rather than a state a test can set up without injecting a failing
//	writer into production code. render's template-error branch next to them is
//	covered (TestRenderFailsSafelyForAnUndefinedTemplate).
//
// web.go: New's `parse templates` branch
//
//	Templates are embedded and parsed at build time; ParseFS succeeds for every
//	binary that compiles. The branch guards a corrupt embed that cannot exist.
//
// web.go: rebuildSnapshot's freshness re-check, and metadataSnapshot's
// cold-start fallback after a refusal
//
//	The re-check is taken only when another handler builds the snapshot while
//	this one waits on snapshotMu; reaching it needs a synchronized interleave
//	across two requests, which a test cannot schedule deterministically. The
//	fallback below it is reachable only when a refusal finds no published
//	snapshot, and every refusal requires a token this session already spent or
//	a slot another operation holds — both of which mean a walk already
//	published one.
//
// objects.go: prepareObjectRow's `TypeLabel = "unreadable"`
//
//	The row's Unreadable flag is set only by a snapshot entry whose metadata
//	read failed. internal/index.Header reports a store's ordinary unreadable
//	answer — cas.ErrCorrupt — as "no header" rather than as an error, and the
//	fs backend's List skips directories, so a listed entry that reaches the
//	row builder with the flag set does not exist for any store the viewer can
//	be pointed at. The rendering of an unreadable row is covered separately
//	(TestUnreadableObjectRowSaysSo builds the row directly); what is uncovered
//	is only the path that would populate the flag.

// --- Branches whose input cannot occur ---

// format.go: formatChecked's zero-time return
//
//	Every caller passes the check time setVerification stored, which stamps
//	time.Now(). The zero guard is defensive.
//
// format.go: shortDigest's short-input return
//
//	Every caller passes a digest the store listed, which is always wider than
//	the eight-character short form. The guard is defensive.
//
// format.go: Version's pseudo-version return
//
//	The branch fires for a build whose main module carries a semantic version.
//	debug.ReadBuildInfo reports "(devel)" for the test binary, and the test
//	binary is never built as a versioned dependency of itself, so the condition
//	cannot be satisfied from inside this module's own tests.
//
// limit.go: refillIn's "already refilled" return
//
//	It returns the cooldown when the budget's next refill is not in the future.
//	begin calls it only after budgetLocked refilled the bucket for every
//	elapsed whole cooldown, so a zero-token bucket is always waiting on a
//	refill that is still ahead. The branch covers an ordering the limiter does
//	not produce.
