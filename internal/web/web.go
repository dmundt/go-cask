// Package web implements the embedded technical viewer (internal/web): the
// browser-facing hypermedia surface at /viewer/* — login with the startup
// token, session cookies, role authorization, CSRF-protected mutations,
// htmx fragments and object pages — per
// viewer-design and viewer-security (which MUST NOT be weakened).
//
// This file holds the server itself: construction, routing, the security
// headers, the static assets, and template rendering. The rest of the package
// is split by concern — auth.go (login and authorization), browser.go (the
// object browser's query state), objects.go (its rows and handlers),
// verify.go (integrity checks), format.go (presentation helpers), and
// sessions.go, meta.go, csrf.go, throttle.go.
package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/index"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed htmx.min.js
var htmxJS []byte

//go:embed viewer.css
var viewerCSS []byte

// Config selects viewer behavior. The viewer runs only when the caller
// (`cmd/cask web`) constructs it — there is no Enabled switch
// (viewer-security §3).
type Config struct {
	// Hasher validates and verifies object digests. A nil value keeps the
	// historical SHA-256 default; callers using another digest algorithm must
	// provide its Hasher.
	Hasher cas.Hasher
	// HashAlgorithm is the display name for Hasher in the object inspector.
	// It defaults to "sha256" when Hasher is omitted.
	HashAlgorithm string
	// StartupToken authenticates the admin login: `cmd/cask web` generates it or
	// takes it from -token-file/CASK_VIEWER_TOKEN, and never logs it.
	StartupToken string
	// RoleTokens maps role names to bearer tokens.
	RoleTokens map[string]string
	// References provides host-maintained graph edges for the optional
	// References inspector tab. Nil leaves references unavailable.
	References ReferenceIndex
	// Reachability provides host-computed root reachability for the optional
	// Orphaned object state. Nil leaves orphan status unavailable.
	Reachability ReachabilityIndex
	// TrustedProxies lists the reverse proxies whose forwarded client address
	// the viewer may believe, as CIDR blocks ("10.0.0.0/8") or single IPs
	// ("127.0.0.1", "::1"). Empty — the default — trusts none, so the login
	// throttle keys on the direct peer address alone. A value that is neither
	// an IP nor a CIDR block fails New rather than silently trusting nothing
	// (viewer-security §5.2).
	TrustedProxies []string
}

// ReferenceIndex supplies application-level graph edges to the viewer. The
// viewer never infers references from opaque CAS payloads and never mutates
// this source; the host owns its lifecycle and completeness.
type ReferenceIndex interface {
	// Inbound returns objects that reference the given digest.
	Inbound(cas.Digest) []cas.Digest
	// Outbound returns objects referenced by the given digest.
	Outbound(cas.Digest) []cas.Digest
}

// ReachabilityIndex reports whether an object is reachable from host-owned
// roots. The viewer never derives reachability from inbound-reference counts.
type ReachabilityIndex interface {
	// IsReachable reports whether the given digest is reachable from roots.
	IsReachable(cas.Digest) bool
}

// Server is the viewer: login, sessions, role authorization, CSRF, and the
// hypermedia pages/fragments.
type Server struct {
	store         *fs.Backend
	cfg           Config
	sessions      *sessions
	loginThrottle *throttle
	meta          *metaCache
	// trusted is the parsed Config.TrustedProxies: the peers whose forwarded
	// client address the login throttle may believe (viewer-security §5.2).
	trusted *trustedProxy
	// snapshot is the published metadata snapshot together with the time it
	// was built. The pair travels as one value, so a reader that loads it
	// never sees a snapshot under the wrong timestamp.
	snapshot atomic.Pointer[snapshotState]
	// snapshotMu serializes snapshot builds. It is only taken once a reader
	// has found the published snapshot missing or stale, so the steady state
	// is a lock-free load.
	snapshotMu sync.Mutex
	tmpl       *template.Template
}

const snapshotRefreshInterval = 500 * time.Millisecond

// snapshotState is a built metadata snapshot and the instant it was built.
type snapshotState struct {
	// snapshot is the built index.
	snapshot *index.Snapshot
	// builtAt is when the store walk that produced snapshot finished.
	builtAt time.Time
}

// metadataSnapshot reuses one bounded metadata walk for the short bursts of
// requests produced by filtering and htmx refreshes. The store remains
// authoritative; expiry keeps externally-added objects visible without
// changing any public storage behavior.
//
// The published snapshot is read without the lock, and the store walk runs
// under it, so a request that arrives while a build is in flight never shares
// the walk and never blocks a reader that already has a fresh snapshot. The
// freshness rule and the error contract are unchanged: a failed build reports
// its error and publishes nothing, so the next caller retries.
func (s *Server) metadataSnapshot(ctx context.Context) (*index.Snapshot, error) {
	if current := s.snapshot.Load(); current != nil && time.Since(current.builtAt) < snapshotRefreshInterval {
		return current.snapshot, nil
	}
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	// Another handler may have built the snapshot while this one waited for
	// the lock, so the fast path is checked again before walking the store.
	if current := s.snapshot.Load(); current != nil && time.Since(current.builtAt) < snapshotRefreshInterval {
		return current.snapshot, nil
	}
	snapshot, err := index.BuildSnapshot(ctx, s.store)
	if err != nil {
		return nil, err
	}
	s.snapshot.Store(&snapshotState{snapshot: snapshot, builtAt: time.Now()})
	return snapshot, nil
}

// New builds the viewer over the raw store. StartupToken is generated or
// supplied by the caller (cmd/cask), which never logs it.
func New(store *fs.Backend, cfg Config) (*Server, error) {
	if cfg.Hasher == nil {
		cfg.Hasher = sha256.New()
	}
	if cfg.HashAlgorithm == "" {
		cfg.HashAlgorithm = "sha256"
	}
	trusted, err := newTrustedProxy(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("viewer trusted proxies: %w", err)
	}
	tmpl, err := template.New("viewer").Funcs(template.FuncMap{
		// The viewer's short form is the first 8 hex characters of a digest
		// (cas.Digest.Prefix); templates that render a digest they hold as a
		// string use this, while the list rows use the precomputed
		// objectRow.Short.
		"shortDigest": shortDigest,
		"formatBytes": formatBytes,
		// The top bar is rendered from several page payloads that share only a
		// CSRF token, so the Verify control builds its own state from it.
		"verifyAll": func(csrf string) verifyAllState {
			return verifyAllState{CSRF: csrf, Label: "Verify"}
		},
		"version": Version,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Server{
		store:         store,
		cfg:           cfg,
		sessions:      newSessions(),
		loginThrottle: newThrottle(5, time.Minute),
		meta:          newMetaCache(),
		trusted:       trusted,
		tmpl:          tmpl,
	}, nil
}

// Handler returns the viewer routes with the fixed middleware order:
// auth (session) → role → CSRF (mutations) → handler. Login is public.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /viewer/login", s.loginPage)
	mux.HandleFunc("POST /viewer/login", s.loginPost)
	mux.HandleFunc("GET /viewer/static/htmx.min.js", s.htmxScript)
	mux.HandleFunc("GET /viewer/static/viewer.css", s.stylesheet)
	mux.HandleFunc("GET /viewer/{$}", s.landing)
	mux.HandleFunc("GET /viewer/objects", s.require(RoleViewer, s.objects))
	mux.HandleFunc("GET /viewer/objects/{hash}", s.require(RoleViewer, s.objectPermalink))
	mux.HandleFunc("GET /viewer/objects/{hash}/dump", s.require(RoleViewer, s.objectDump))
	mux.HandleFunc("POST /viewer/objects/verify", s.require(RoleOperator, s.verifyAllFragment))
	mux.HandleFunc("POST /viewer/objects/{hash}/verify", s.require(RoleOperator, s.verifyFragment))
	// Everything else under the prefix is not a viewer route. The catch-all
	// names no method, so a path the viewer never served answers the same way
	// whichever verb asks for it -- including the delete and GC routes the
	// viewer deliberately does not have, since destroying an object is a
	// store-lifecycle operation that belongs to the CLI. It answers through
	// s.require so the reply turns on the caller's session rather than on the
	// path: 401 without one, 404 with. A bare 404 would let an anonymous
	// caller map which paths the viewer knows.
	mux.HandleFunc("/viewer/", s.require(RoleViewer, http.NotFound))
	return secureHeaders(mux)
}

// landing is the viewer's entry point. It completes the documented `?token=`
// deep link (viewer-security §5.1) when the request is same-origin, sends a
// caller without a session to the login page so a browser can reach it, and
// otherwise shows the object browser. A token on a request that is not
// same-origin is refused by loginToken before any session exists, so a
// cross-site <img>, <link>, or navigation cannot mint one. It is registered
// for the exact path: every other path under the prefix belongs to a named
// route or to the catch-all.
func (s *Server) landing(w http.ResponseWriter, r *http.Request) {
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		s.loginToken(w, r, token)
		return
	}
	if _, ok := s.sessions.get(sessionID(r)); !ok {
		http.Redirect(w, r, "/viewer/login", http.StatusSeeOther)
		return
	}
	s.require(RoleViewer, s.objects)(w, r)
}

// secureHeaders applies the response hardening every viewer response carries.
// The viewer serves its own stylesheet and its only script from its own
// origin, so the policy can deny everything else outright: no third-party
// script can be injected, the pages cannot be framed, and a browser cannot be
// talked into treating a hexdump as a script by sniffing it.
//
// No viewer response is cacheable. Handler output is per-request and mostly
// session-scoped — digests, object bytes, and the session's verification state
// — so a cache between the viewer and the browser (the TLS-terminating proxy a
// remote deployment puts in front of it, viewer-security §12) must not retain
// it. The pages that do turn on the session cookie say so with Vary, so an
// intermediary cannot answer a later caller with a page rendered for someone
// else's session.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Cache-Control", "no-store")
		header.Add("Vary", "Cookie")
		header.Set("X-Content-Type-Options", "nosniff")
		// style-src allows inline styles because htmx injects a style element
		// for its indicator class; script-src stays strict, which is the
		// directive that matters for injection.
		header.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"connect-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; "+
				"frame-ancestors 'none'")
		header.Set("X-Frame-Options", "DENY")
		// The documented `?token=` deep link puts a credential in the URL, so
		// no viewer response may carry its own URL onward as a Referer
		// (viewer-security §11). Setting it here covers every response rather
		// than only the one route that can be reached with a token in hand.
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) htmxScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	if _, err := w.Write(htmxJS); err != nil {
		slog.Error("viewer htmx write", "err", err)
	}
}

func (s *Server) stylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if _, err := w.Write(viewerCSS); err != nil {
		slog.Error("viewer css write", "err", err)
	}
}

// --- helpers ---

// render executes the named template into a buffer first, so a template error
// yields a clean 500 instead of a half-written 200.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render", "template", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Error("render write", "template", name, "err", err)
	}
}

type shellData struct {
	// View names the content template rendered by the shell.
	View string
	// Data is the view-specific template data.
	Data any
}

// renderPage renders a complete viewer document through the single shell.
// Page-specific templates are content components; only shell owns the document
// and body structure.
func (s *Server) renderPage(w http.ResponseWriter, view string, data any) {
	s.render(w, "shell", shellData{View: view, Data: data})
}

// previewLimit bounds the hexdump preview; larger objects are truncated.
const previewLimit = 256

// readN reads at most n bytes from the object at d.
func (s *Server) readN(ctx context.Context, d cas.Digest, n int64) ([]byte, error) {
	rc, err := s.store.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, n))
}

// readPreview reads at most previewLimit bytes and reports whether the object
// was truncated.
func (s *Server) readPreview(ctx context.Context, d cas.Digest) ([]byte, bool, error) {
	data, err := s.readN(ctx, d, previewLimit+1)
	if err != nil {
		return nil, false, err
	}
	if len(data) > previewLimit {
		return data[:previewLimit], true, nil
	}
	return data, false, nil
}

func (s *Server) parseDigest(w http.ResponseWriter, r *http.Request) (cas.Digest, bool) {
	d, err := cas.ParseDigest(r.PathValue("hash"))
	if err != nil {
		http.Error(w, "malformed hash", http.StatusBadRequest)
		return nil, false
	}
	if err := s.cfg.Hasher.Validate(d); err != nil {
		http.Error(w, "malformed hash", http.StatusBadRequest)
		return nil, false
	}
	return d, true
}
