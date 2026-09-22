package web

import (
	"bytes"
	"cmp"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
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
	// StartupToken is generated at startup and printed once for admin login.
	StartupToken string
	// RoleTokens maps role names to bearer tokens.
	RoleTokens map[string]string
	// References provides host-maintained graph edges for the optional
	// References inspector tab. Nil leaves references unavailable.
	References ReferenceIndex
	// Reachability provides host-computed root reachability for the optional
	// Orphaned object state. Nil leaves orphan status unavailable.
	Reachability ReachabilityIndex
}

// ReferenceIndex supplies application-level graph edges to the viewer. The
// viewer never infers references from opaque CAS payloads and never mutates
// this source; the host owns its lifecycle and completeness.
type ReferenceIndex interface {
	Inbound(cas.Digest) []cas.Digest
	Outbound(cas.Digest) []cas.Digest
}

// ReachabilityIndex reports whether an object is reachable from host-owned
// roots. The viewer never derives reachability from inbound-reference counts.
type ReachabilityIndex interface {
	IsReachable(cas.Digest) bool
}

func formatWritten(written time.Time) string {
	return formatWrittenAt(written, time.Now())
}

func formatWrittenAt(written, now time.Time) string {
	if written.IsZero() {
		return ""
	}
	age := now.Sub(written)
	if age < 0 {
		age = 0
	}
	minutes := int(age / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm ago", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh ago", hours)
	}
	return fmt.Sprintf("%dd ago", hours/24)
}

// formatChecked renders a verification timestamp. A verification result is
// only as good as its age, so the label states the clock time and the elapsed
// age together.
func formatChecked(checked time.Time) string {
	if checked.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s)", checked.UTC().Format("2006-01-02 15:04:05 UTC"), formatWritten(checked))
}

// checkedLabel renders when an object was last verified in this session.
func checkedLabel(store *sessions, id, digest string) string {
	_, checked := store.verificationRecord(id, digest)
	return formatChecked(checked)
}

// storedReport replays the last check of digest in this session, or nil when
// there is none. The check time is stamped on at render time because the label
// carries a relative age that keeps moving after the check ran.
func storedReport(store *sessions, id, digest string) *actionOutcome {
	_, checked, report := store.verificationReport(id, digest)
	if checked.IsZero() {
		return nil
	}
	report.Checked = formatChecked(checked)
	return &report
}

func formatTimestamp(written time.Time) string {
	if written.IsZero() {
		return ""
	}
	return written.UTC().Format(time.RFC3339Nano)
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 10 || value == math.Trunc(value) {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func shortDigest(d cas.Digest) string {
	prefix := d.Prefix(8)
	if len(d.String()) <= len(prefix) {
		return prefix
	}
	return prefix + "…"
}

// Version reports the build's module version, rendered as the viewer and the
// CLI both show it. It comes from build info, so it is a pseudo-version until
// the first tag and "dev" for an untracked build (versioning §2).
func Version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return "dev"
}

// selectionLinkAttrs renders the attributes every link that selects an object
// carries: the href for a cold click, the htmx request that swaps the inspector
// in place, and the header that marks the request as a selection. Row cells,
// inspector tabs, history arrows, and reference links all navigate the same
// way, so they all emit these from here — a copy that quietly lost the
// selection header would still look right. The URL is escaped because the
// result is injected as raw attribute text.
func selectionLinkAttrs(selectURL string) template.HTMLAttr {
	escaped := template.HTMLEscapeString(selectURL)
	return template.HTMLAttr(fmt.Sprintf(
		`href="%s" hx-get="%s" hx-target="#object-inspector" hx-headers='{"X-Viewer-Selection":"true"}' hx-push-url="true"`,
		escaped, escaped))
}

// Server is the viewer: login, sessions, role authorization, CSRF, and the
// hypermedia pages/fragments.
type Server struct {
	store         *fs.Backend
	cfg           Config
	sessions      *sessions
	loginThrottle *throttle
	meta          *metaCache
	tmpl          *template.Template
}

// New builds the viewer over the raw store. StartupToken is generated by the
// caller (cmd/cask) and printed once at startup.
func New(store *fs.Backend, cfg Config) (*Server, error) {
	tmpl, err := template.New("viewer").Funcs(template.FuncMap{
		// The viewer's short form is the first 8 hex characters of a digest
		// (cas.Digest.Prefix); templates that render a digest they hold as a
		// string use this, while the list rows use the precomputed
		// objectRow.Short.
		"shortDigest": shortDigest,
		"formatBytes": formatBytes,
		// Every link that selects an object navigates the same way, and one
		// page emits a dozen of them: seven cells per row, three inspector
		// tabs, two history arrows, and one per reference. Emitting the shared
		// attributes from one place keeps a copy from drifting.
		"selectLink": selectionLinkAttrs,
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
		tmpl:          tmpl,
	}, nil
}

// Handler returns the viewer routes with the fixed middleware order:
// auth (session) → role → CSRF (mutations) → handler. Login is public.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /viewer/login", s.loginPage)
	mux.HandleFunc("POST /viewer/login", s.loginPost)
	mux.HandleFunc("GET /viewer/static/htmx.min.js", s.htmx)
	mux.HandleFunc("GET /viewer/static/viewer.css", s.css)
	mux.HandleFunc("GET /viewer/", func(w http.ResponseWriter, r *http.Request) {
		// Never let a token in the URL leak via Referer.
		w.Header().Set("Referrer-Policy", "no-referrer")
		if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
			s.loginToken(w, r, token)
			return
		}
		if _, ok := s.sessions.get(sessionID(r)); !ok {
			http.Redirect(w, r, "/viewer/login", http.StatusSeeOther)
			return
		}
		s.require(RoleViewer, s.objects)(w, r)
	})
	// These routes exist only to keep the "GET /viewer/" prefix from swallowing
	// paths the viewer no longer serves. They answer through s.require so an
	// anonymous caller is sent to the login page like everywhere else: a bare
	// 404 would tell an unauthenticated caller which paths the viewer knows.
	mux.HandleFunc("GET /viewer/dashboard", s.require(RoleViewer, http.NotFound))
	mux.HandleFunc("GET /viewer/dashboard/", s.require(RoleViewer, http.NotFound))
	mux.HandleFunc("GET /viewer/gc", s.require(RoleViewer, http.NotFound))
	mux.HandleFunc("POST /viewer/gc", s.require(RoleOperator, http.NotFound))
	mux.HandleFunc("GET /viewer/objects", s.require(RoleViewer, s.objects))
	mux.HandleFunc("GET /viewer/objects/{hash}", s.require(RoleViewer, s.objectDetail))
	mux.HandleFunc("GET /viewer/objects/{hash}/raw", s.require(RoleViewer, s.objectRaw))
	mux.HandleFunc("POST /viewer/objects/verify-all", s.require(RoleOperator, s.verifyAllFragment))
	mux.HandleFunc("POST /viewer/objects/{hash}/verify", s.require(RoleOperator, s.verifyFragment))
	// The viewer inspects; it does not destroy. Deleting an object is a
	// store-lifecycle operation that belongs to the CLI, where it can be
	// scripted, audited, and paired with the roots a sweep needs.
	mux.HandleFunc("POST /viewer/objects/{hash}/delete", s.require(RoleOperator, http.NotFound))
	return secureHeaders(mux)
}

// secureHeaders applies the response hardening every viewer response carries.
// The viewer serves its own stylesheet and its only script from its own
// origin, so the policy can deny everything else outright: no third-party
// script can be injected, the pages cannot be framed, and a browser cannot be
// talked into treating a hexdump as a script by sniffing it.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		// style-src allows inline styles because htmx injects a style element
		// for its indicator class; script-src stays strict, which is the
		// directive that matters for injection.
		header.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"connect-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; "+
				"frame-ancestors 'none'")
		header.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// Roles (viewer-security §8).
const (
	// RoleViewer permits read-only viewer access.
	RoleViewer = "viewer"
	// RoleOperator permits verification operations.
	RoleOperator = "operator"
	// RoleAdmin is the highest rank. The viewer exposes no destructive
	// operation, so it currently gates nothing the operator rank does not
	// already reach; it stays because the ladder, not the viewer, defines it.
	RoleAdmin = "admin"
)

// require enforces: valid session (401 empty), sufficient role (403 empty),
// CSRF on mutations (403 empty). Never discloses existence.
func (s *Server) require(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessions.get(sessionID(r))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized) // empty body
			return
		}
		if sess.Role != role && !roleAllows(sess.Role, role) {
			w.WriteHeader(http.StatusForbidden) // empty body
			return
		}
		if !csrfOK(r, sess) {
			slog.Warn("viewer csrf rejected", "path", r.URL.Path, "session", sessionHandle(sess.ID))
			w.WriteHeader(http.StatusForbidden) // empty body
			return
		}
		next(w, r)
	}
}

// roleAllows reports whether the session role satisfies the required role
// (admin ⊃ operator ⊃ viewer).
func roleAllows(sessionRole, required string) bool {
	rank := map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}
	return rank[sessionRole] >= rank[required]
}

// --- login ---

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "login", nil)
}

// loginPost validates the submitted token against the startup token (admin)
// or the configured per-role tokens, throttles failures per IP (5/min with
// backoff, viewer-security §5), and issues a session cookie.
func (s *Server) loginToken(w http.ResponseWriter, r *http.Request, token string) {
	ip := callerIP(r)
	if !s.loginThrottle.allow(ip) {
		slog.Warn("viewer login throttled", "ip", ip)
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	role, ok := s.resolveToken(token)
	if !ok {
		// The attempt was already recorded by allow.
		slog.Warn("viewer login failed", "ip", ip) // token value never logged
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	s.loginThrottle.reset(ip)
	sess, err := s.sessions.create(role)
	if err != nil {
		slog.Error("viewer login", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, sess)
	slog.Info("viewer login", "role", role, "ip", ip, "session", sessionHandle(sess.ID))
	http.Redirect(w, r, "/viewer/", http.StatusSeeOther)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	s.loginToken(w, r, r.FormValue("token"))
}

// resolveToken matches token against the startup token (admin role) and the
// configured per-role tokens (token → role) using constant-time comparison.
// An empty token never matches, so a mis-configured empty token cannot grant
// a role (viewer-security §5).
func (s *Server) resolveToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.StartupToken)) == 1 {
		return RoleAdmin, true
	}
	for tok, role := range s.cfg.RoleTokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(token)) == 1 {
			return role, true
		}
	}
	return "", false
}

type objectRow struct {
	// hash is the row's address as the store listed it. Keeping it spares the
	// inspector from parsing the rendered Digest back into a digest.
	hash cas.Digest
	// Digest is the full object digest.
	Digest string
	// Short is the abbreviated digest.
	Short string
	// Type is the decoded object type.
	Type string
	// TypeLabel is what the type cell shows. It differs from Type only when the
	// bytes could not be read: the cell says so instead of rendering an empty
	// cell that reads like an untyped object.
	TypeLabel string
	// Unreadable reports that the object's bytes could not be read.
	Unreadable bool
	// Size is the stored payload size.
	Size int64
	// References is the number of host-indexed inbound references, when known.
	References int
	// ReferencesAvailable reports whether References came from the configured source.
	ReferencesAvailable bool
	// Integrity is the session-scoped integrity result, independent of reachability.
	Integrity string
	// IntegrityLabel is the human-readable integrity result.
	IntegrityLabel string
	// Orphaned reports that no configured root reaches this object.
	Orphaned bool
	// ReachabilityKnown reports whether Orphaned was computed at all. The
	// reference column is a row-level decision because the row template only
	// ever sees the row.
	ReachabilityKnown bool
	// Written is physical object write metadata from the filesystem backend.
	Written time.Time
	// WrittenLabel is the formatted physical write time.
	WrittenLabel string
	// Selected reports whether this row backs the visible inspector.
	Selected bool
	// SelectURL opens this row in the browser inspector.
	SelectURL string
}

// --- objects list ---

const (
	defaultObjectLimit = 25
	maxObjectLimit     = 250
)

type objectBrowserState struct {
	Query     string
	Type      string
	Size      string
	Status    string
	Reach     string
	Sort      string
	Direction string
	Limit     int
	Offset    int
	// OffsetSet records an explicitly requested page. Without it the browser
	// is free to page to whichever rows the selection lives on.
	OffsetSet bool
	Selected  string
	// Deselected records an explicitly empty selection: the operator clicked
	// the selected row again. It is distinct from an absent one, which still
	// auto-selects the first visible row.
	Deselected bool
	// Nav records how the operator arrived at this selection, which decides
	// what happens to the session trail. It describes a single click, so it
	// is never carried by url(): only navURL emits it.
	Nav string
	Tab string
}

// Navigation markers. An absent marker means the selection did not come from
// the inspector, so the trail restarts at it.
const (
	navReference = "ref"
	navTrail     = "trail"
	// navStay marks a click that re-renders the same selection — switching
	// inspector tabs. The selection did not move, so the trail must not.
	navStay = "stay"
)

func defaultObjectBrowserState() objectBrowserState {
	return objectBrowserState{
		Sort:      "hash",
		Direction: "asc",
		Limit:     defaultObjectLimit,
		Tab:       "metadata",
	}
}

// The object browser's closed value sets. Each axis names its own so the parser
// checks every one the same way, and so the filter controls and the parser can
// never offer and accept different things.
var (
	// objectSizeBands lists the size filter's buckets, which matchesObjectRow
	// turns into byte ranges.
	objectSizeBands = []string{"small", "medium", "large"}
	// objectReachStates lists the reachability filter's choices. Reachability
	// is a separate axis from integrity and has its own filter.
	objectReachStates = []string{"reachable", "orphaned"}
	// objectDirections lists the sort orders.
	objectDirections = []string{"asc", "desc"}
	// objectNavModes lists the navigation markers a selection may carry.
	objectNavModes = []string{navReference, navTrail, navStay}
	// objectTabs lists the inspector's panels.
	objectTabs = []string{"metadata", "references", "bytes"}
)

func parseObjectBrowserState(values url.Values) (objectBrowserState, error) {
	state := defaultObjectBrowserState()
	var err error
	if state.Query, err = queryValue(values, "q"); err != nil {
		return state, err
	}
	state.Query = strings.ToLower(strings.TrimSpace(state.Query))
	if state.Type, err = queryValue(values, "type"); err != nil {
		return state, err
	}
	if state.Size, err = enumValue(values, "size", "", objectSizeBands); err != nil {
		return state, err
	}
	if state.Status, err = enumValue(values, "status", "", objectStates); err != nil {
		return state, err
	}
	if state.Reach, err = enumValue(values, "reach", "", objectReachStates); err != nil {
		return state, err
	}
	if state.Sort, err = enumValue(values, "sort", "hash", objectSortKeys); err != nil {
		return state, err
	}
	if state.Direction, err = enumValue(values, "dir", "asc", objectDirections); err != nil {
		return state, err
	}
	if state.Limit, err = queryInt(values, "limit", defaultObjectLimit); err != nil {
		return state, err
	}
	// A page size is a preference, not an assertion about the store, so an
	// out-of-range one is clamped rather than rejected: a hand-edited or
	// stale URL should still render a page. Only a non-numeric limit is a
	// malformed query, and queryInt has already rejected that.
	state.Limit = min(max(state.Limit, 1), maxObjectLimit)
	if state.Offset, err = queryInt(values, "offset", 0); err != nil || state.Offset < 0 {
		return state, fmt.Errorf("invalid offset")
	}
	_, state.OffsetSet = values["offset"]
	if state.Selected, err = queryValue(values, "selected"); err != nil {
		return state, err
	}
	if state.Selected != "" {
		digest, parseErr := sha256.Parse(state.Selected)
		if parseErr != nil {
			return state, fmt.Errorf("invalid selected object: %w", parseErr)
		}
		state.Selected = digest.String()
	} else if _, present := values["selected"]; present {
		state.Deselected = true
	}
	if state.Nav, err = enumValue(values, "nav", "", objectNavModes); err != nil {
		return state, err
	}
	if state.Tab, err = queryValue(values, "tab"); err != nil {
		return state, err
	}
	// "actions" was a tab of its own before the metadata panel absorbed it. A
	// bookmark naming it is honoured rather than rejected, so it is rewritten
	// before the tab is checked.
	if state.Tab == "actions" {
		state.Tab = "metadata"
	}
	if state.Tab, err = checkEnum("tab", state.Tab, "metadata", objectTabs); err != nil {
		return state, err
	}
	return state, nil
}

// objectSortKeys lists every sortable column. It is derived from the header
// description below so a key the URL accepts and a column the table renders
// can never disagree.
var objectSortKeys = func() []string {
	keys := make([]string, 0, len(objectSortColumns))
	for _, column := range objectSortColumns {
		keys = append(keys, column.Key)
	}
	return keys
}()

// objectSortColumns describes the object table's header, in display order. It
// is the single source for both the sortable keys and the markup: seven
// hand-written copies of the same header is how the Hash column's accessible
// name drifted out of step with its own arrow.
var objectSortColumns = []struct {
	// Key is the sort key this column requests, and the value parsed back out
	// of the "sort" query parameter.
	Key string
	// Label is the visible column heading.
	Label string
	// Name is the column as the accessible name says it, which is not always
	// the heading: "Inbound" counts inbound references and says so.
	Name string
	// Class is the <th> class, empty when the column needs none.
	Class string
	// NeedsReachability marks a column the viewer can only render when the
	// host supplies a reachability index.
	NeedsReachability bool
}{
	{Key: "hash", Label: "Hash", Name: "hash"},
	{Key: "type", Label: "Type", Name: "type"},
	{Key: "size", Label: "Size", Name: "size"},
	{Key: "inbound", Label: "Inbound", Name: "inbound references", Class: "viewer-references"},
	{Key: "status", Label: "Integrity", Name: "integrity", Class: "viewer-status-cell"},
	{Key: "reach", Label: "References", Name: "references", Class: "viewer-status-cell", NeedsReachability: true},
	{Key: "written", Label: "Written", Name: "written"},
}

// sortColumn is one rendered table header. Every decision the markup would
// otherwise make is resolved here, so the template carries no per-column logic.
type sortColumn struct {
	Label string
	Name  string
	Class string
	// URL sorts by this column: it flips the direction when the column already
	// owns the sort and starts ascending otherwise.
	URL string
	// Active reports that this column owns the current sort.
	Active bool
	// AriaSort is the active column's current order, empty for the rest.
	AriaSort string
	// NextDirection names the order the next click applies. An inactive column
	// always starts ascending, whatever the active column is doing.
	NextDirection string
	// Glyph is the direction indicator: the active column's current order, and
	// the ascending arrow everywhere else.
	Glyph string
}

// sortColumns renders the table header for state, dropping the reachability
// column when the host supplies no index for it.
func sortColumns(state objectBrowserState, hasReachability bool) []sortColumn {
	columns := make([]sortColumn, 0, len(objectSortColumns))
	for _, def := range objectSortColumns {
		if def.NeedsReachability && !hasReachability {
			continue
		}
		column := sortColumn{
			Label:         def.Label,
			Name:          def.Name,
			Class:         def.Class,
			URL:           sortURL(state, def.Key),
			Active:        state.Sort == def.Key,
			NextDirection: "ascending",
			Glyph:         "▲",
		}
		if column.Active {
			column.AriaSort = state.Direction + "ending"
			if state.Direction == "asc" {
				column.NextDirection = "descending"
			} else {
				column.Glyph = "▼"
			}
		}
		columns = append(columns, column)
	}
	return columns
}

// objectStates lists the selectable integrity filters in display order.
// Reachability is the other, independent axis and has its own filter.
var objectStates = []string{"verified", "not-verified", "corrupt"}

func queryValue(values url.Values, key string) (string, error) {
	all, ok := values[key]
	if !ok {
		return "", nil
	}
	if len(all) != 1 {
		return "", fmt.Errorf("repeated %s", key)
	}
	return all[0], nil
}

func queryInt(values url.Values, key string, fallback int) (int, error) {
	value, err := queryValue(values, key)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

// enumValue reads a query parameter whose values form a closed set.
func enumValue(values url.Values, key, fallback string, allowed []string) (string, error) {
	value, err := queryValue(values, key)
	if err != nil {
		return "", err
	}
	return checkEnum(key, value, fallback, allowed)
}

// checkEnum resolves one closed-set value. An absent value reads as the
// fallback, and so does an empty one: a filter control reset to "All" submits
// an empty string. Anything else outside the set is a malformed query rather
// than a filter that happens to match nothing — the viewer says so instead of
// rendering a plausible but wrong empty page.
func checkEnum(key, value, fallback string, allowed []string) (string, error) {
	if value == "" {
		return fallback, nil
	}
	if !slices.Contains(allowed, value) {
		return "", fmt.Errorf("invalid %s", key)
	}
	return value, nil
}

type objectBrowserData struct {
	State           objectBrowserState
	RefreshURL      string
	HasReachability bool
	StatusOptions   []filterOption
	LimitOptions    []filterOption
	Objects         []objectRow
	Types           []filterOption
	HasAny          bool
	Total           int
	Matched         int
	TotalSize       int64
	RangeStart      int
	RangeEnd        int
	CurrentPage     int
	PageCount       int
	FirstURL        string
	PreviousURL     string
	NextURL         string
	LastURL         string
	// SortColumns is the table header: one entry per rendered column, already
	// resolved into the link, the arrow, and the accessible name it needs.
	SortColumns []sortColumn
	HasPrevious bool
	HasNext     bool
	Inspector   *browserInspector
	CSRF        string
	Role        string
}

type filterOption struct {
	Value    string
	Label    string
	Selected bool
}

type browserInspector struct {
	Digest         string
	Type           string
	Size           int64
	Integrity      string
	IntegrityLabel string
	// Report is the finding of the last check of this object in this session,
	// replayed so the inspector states the outcome and its age on every visit
	// rather than only in the response to the click that produced it. It is nil
	// when the object has not been checked.
	Report              *actionOutcome
	Orphaned            bool
	WrittenLabel        string
	InboundReferences   int
	ReferencesAvailable bool
	Inbound             []referenceRow
	Outbound            []referenceRow
	Timestamp           string
	RawURL              string
	MetadataURL         string
	BytesURL            string
	ReferencesURL       string
	// PrevURL and NextURL step through the objects this session already
	// inspected; an empty one disables that control.
	PrevURL string
	NextURL string
}

type referenceRow struct {
	Digest    string
	Short     string
	Type      string
	SelectURL string
}

func (state objectBrowserState) url() string {
	values := url.Values{}
	if state.Query != "" {
		values.Set("q", state.Query)
	}
	if state.Type != "" {
		values.Set("type", state.Type)
	}
	if state.Size != "" {
		values.Set("size", state.Size)
	}
	if state.Status != "" {
		values.Set("status", state.Status)
	}
	if state.Reach != "" {
		values.Set("reach", state.Reach)
	}
	if state.Sort != "hash" {
		values.Set("sort", state.Sort)
	}
	if state.Direction != "asc" {
		values.Set("dir", state.Direction)
	}
	if state.Limit != defaultObjectLimit {
		values.Set("limit", strconv.Itoa(state.Limit))
	}
	if state.Offset != 0 || state.OffsetSet {
		values.Set("offset", strconv.Itoa(state.Offset))
	}
	if state.Selected != "" {
		values.Set("selected", state.Selected)
	} else if state.Deselected {
		values.Set("selected", "")
	}
	if state.Tab != "metadata" {
		values.Set("tab", state.Tab)
	}
	encoded := values.Encode()
	if encoded == "" {
		return "/viewer/objects"
	}
	return "/viewer/objects?" + encoded
}

// navURL renders the state and marks how the operator arrived at it. The
// marker lives outside url() on purpose: it describes one click, so it must
// never survive into the filter, sort, or pager links built from the same
// state.
func (state objectBrowserState) navURL(mode string) string {
	raw := state.url()
	separator := "?"
	if strings.Contains(raw, "?") {
		separator = "&"
	}
	return raw + separator + "nav=" + mode
}

// objects renders the object browser. It is a pipeline: read the request into
// a state, turn the store into the rows that state matches, page and select
// among them, then render whichever part of the page the request asked for.
func (s *Server) objects(w http.ResponseWriter, r *http.Request) {
	state, err := parseObjectBrowserState(r.URL.Query())
	if err != nil {
		http.Error(w, "invalid object browser query", http.StatusBadRequest)
		return
	}
	if state.Reach != "" && s.cfg.Reachability == nil {
		http.Error(w, "reachability filter unavailable", http.StatusBadRequest)
		return
	}
	id := sessionID(r)
	rows, types, typeFound, total, err := s.objectRows(r.Context(), id, state)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	if !typeFound {
		http.Error(w, "invalid object type", http.StatusBadRequest)
		return
	}
	sortObjectRows(rows, state)
	// The refresher reloads from the URL the request named, so that URL is
	// captured before paging: a page the browser chose on the operator's behalf
	// must not become one they are pinned to.
	refreshURL := state.url()
	page, state := pageObjects(rows, state)
	if page.Defaulted {
		// A defaulted selection is the exception — it has to be named, because
		// the original URL would resurrect the row the filter dropped.
		refreshURL = state.url()
	}
	linkRows(page.Rows, state)
	s.recordTrail(id, state)

	data := objectBrowserData{
		State:           state,
		RefreshURL:      refreshURL,
		HasReachability: s.cfg.Reachability != nil,
		StatusOptions:   statusOptions(state.Status),
		LimitOptions:    limitOptions(state.Limit),
		Types:           typeOptions(types, state.Type),
		Objects:         page.Rows,
		HasAny:          len(page.Rows) > 0,
		Total:           total,
		Matched:         len(rows),
		TotalSize:       totalSize(rows),
		RangeStart:      page.RangeStart,
		RangeEnd:        page.RangeEnd,
		CurrentPage:     state.Offset/state.Limit + 1,
		PageCount:       max((len(rows)+state.Limit-1)/state.Limit, 1),
		FirstURL:        paginationURL(state, 0),
		PreviousURL:     paginationURL(state, max(state.Offset-state.Limit, 0)),
		NextURL:         paginationURL(state, min(state.Offset+state.Limit, max(len(rows)-1, 0))),
		LastURL:         paginationURL(state, max(len(rows)-1, 0)/state.Limit*state.Limit),
		SortColumns:     sortColumns(state, s.cfg.Reachability != nil),
		HasPrevious:     state.Offset > 0 && len(rows) > 0,
		HasNext:         state.Offset+state.Limit < len(rows),
		CSRF:            s.csrfFor(r),
		Role:            s.roleFor(r),
	}
	if page.Selected >= 0 {
		data.Inspector = s.inspectorFor(r.Context(), id, state, rows[page.Selected])
	}
	s.renderObjects(w, r, data)
}

// objectRows builds one row per stored object and keeps the ones state matches.
// It also reports every type present in the store, which the type filter
// offers, and whether the requested type was among them: a filter naming a type
// no object carries is a malformed request, not an empty page.
func (s *Server) objectRows(ctx context.Context, id string, state objectBrowserState) (rows []objectRow, types []string, typeFound bool, total int, err error) {
	digests, err := s.store.List(ctx)
	if err != nil {
		return nil, nil, false, 0, err
	}
	present := make(map[string]bool)
	typeFound = state.Type == ""
	for _, h := range digests {
		row := s.objectRowFor(ctx, id, h)
		if row.Type != "" {
			present[row.Type] = true
		}
		if state.Type == row.Type {
			typeFound = true
		}
		if matchesObjectRow(row, state) {
			rows = append(rows, row)
		}
	}
	return rows, slices.Sorted(maps.Keys(present)), typeFound, len(digests), nil
}

// objectRowFor renders one stored object as a table row.
func (s *Server) objectRowFor(ctx context.Context, id string, h cas.Digest) objectRow {
	meta := s.objectMetaFor(ctx, h)
	row := objectRow{
		hash:   h,
		Digest: h.String(),
		Short:  shortDigest(h),
		// An unreadable object keeps an empty Type so a type filter can never
		// match it — the type is unknown, not blank — while the cell still says
		// what happened.
		Type:              meta.Type,
		TypeLabel:         meta.Type,
		Unreadable:        meta.Unreadable,
		Size:              meta.Size,
		Integrity:         s.sessions.verification(id, h.String()),
		Written:           meta.Written,
		WrittenLabel:      formatWritten(meta.Written),
		ReachabilityKnown: s.cfg.Reachability != nil,
	}
	if meta.Unreadable {
		row.TypeLabel = "unreadable"
	}
	row.IntegrityLabel = integrityLabel(row.Integrity)
	row.Orphaned = row.ReachabilityKnown && !s.cfg.Reachability.IsReachable(h)
	if s.cfg.References != nil {
		row.References = len(s.cfg.References.Inbound(h))
		row.ReferencesAvailable = true
	}
	return row
}

// objectPage is the slice of rows one page shows, and where that slice sits in
// the full result.
type objectPage struct {
	Rows       []objectRow
	RangeStart int
	RangeEnd   int
	// Selected indexes the inspected row within the full result, -1 when the
	// inspector stays empty.
	Selected int
	// Defaulted reports that the browser picked the selection rather than the
	// request naming it, which the refresh URL has to account for.
	Defaulted bool
}

// pageObjects cuts the page out of rows and resolves which row the inspector
// shows, returning the state that describes the result. Paging and selection
// decide each other, so they are resolved together rather than in sequence.
func pageObjects(rows []objectRow, state objectBrowserState) (objectPage, objectBrowserState) {
	if !state.OffsetSet && state.Selected != "" {
		// Following a reference selects a row the current page may not hold, so
		// the browser pages to it. An explicit pager click stays put.
		if i := indexOfDigest(rows, state.Selected); i >= 0 {
			state.Offset = i / state.Limit * state.Limit
		}
	}
	page := objectPage{Selected: -1}
	if state.Offset < len(rows) {
		end := min(state.Offset+state.Limit, len(rows))
		page.RangeStart = state.Offset + 1
		page.RangeEnd = end
		page.Rows = rows[state.Offset:end]
	}
	page.Selected = indexOfDigest(rows, state.Selected)
	// An empty inspector beside a populated list is wasted space, so the first
	// visible row stands in — both before the operator picks one and after a
	// filter drops the one they had picked. A selection that merely sits on
	// another page still stands, so paging never steals it, and an explicit
	// deselect is honoured rather than undone. The URL is left alone: the
	// default is a rendering choice, not navigation.
	if page.Selected < 0 && len(page.Rows) > 0 && !state.Deselected {
		state.Selected = page.Rows[0].Digest
		page.Selected = state.Offset
		page.Defaulted = true
	}
	return page, state
}

// indexOfDigest locates a row by its rendered digest, or -1. An empty digest
// never matches, so a deselected browser finds nothing.
func indexOfDigest(rows []objectRow, digest string) int {
	if digest == "" {
		return -1
	}
	return slices.IndexFunc(rows, func(row objectRow) bool { return row.Digest == digest })
}

// linkRows marks the selected row and points every row at the state its own
// click produces.
func linkRows(rows []objectRow, state objectBrowserState) {
	for i := range rows {
		rowState := state
		rowState.Nav = ""
		rows[i].Selected = rows[i].Digest == state.Selected
		// Clicking the selected row again clears the inspector, so its link
		// points at the deselected state instead of at itself.
		if rows[i].Selected {
			rowState.Selected = ""
			rowState.Deselected = true
		} else {
			rowState.Selected = rows[i].Digest
			rowState.Deselected = false
		}
		rows[i].SelectURL = rowState.url()
	}
}

// recordTrail updates the session trail for the selection state describes. The
// trail records how the operator browsed references: following a reference
// extends it, the Prev/Next controls only move its cursor, and picking a row in
// the table starts over — a table pick is a new point of departure, not a step
// in the chain that led here.
func (s *Server) recordTrail(id string, state objectBrowserState) {
	if state.Selected == "" {
		return
	}
	switch state.Nav {
	case navStay:
		// A tab switch re-renders the same object: nothing to record.
	case navTrail:
		s.sessions.seek(id, state.Selected)
	case navReference:
		s.sessions.visit(id, state.Selected)
	default:
		s.sessions.restart(id, state.Selected)
	}
}

// inspectorFor renders the selected row in the inspector. The row carries the
// digest the store listed, so the reference lookups reuse it rather than
// parsing the rendered form back — a digest that round-trips through the page
// is the same digest, and treating the trip as fallible only produced an error
// path nothing could reach.
func (s *Server) inspectorFor(ctx context.Context, id string, state objectBrowserState, row objectRow) *browserInspector {
	tabURL := func(tab string) string {
		tabbed := state
		tabbed.Tab = tab
		return tabbed.navURL(navStay)
	}
	prevDigest, nextDigest := s.sessions.trailNeighbors(id)
	inspector := &browserInspector{
		Digest:              row.Digest,
		Type:                row.Type,
		Size:                row.Size,
		Integrity:           row.Integrity,
		IntegrityLabel:      row.IntegrityLabel,
		Report:              storedReport(s.sessions, id, row.Digest),
		Orphaned:            row.Orphaned,
		WrittenLabel:        row.WrittenLabel,
		ReferencesAvailable: s.cfg.References != nil,
		Timestamp:           formatTimestamp(row.Written),
		RawURL:              "/viewer/objects/" + row.Digest + "/raw",
		MetadataURL:         tabURL("metadata"),
		BytesURL:            tabURL("bytes"),
		ReferencesURL:       tabURL("references"),
		PrevURL:             trailURL(state, prevDigest),
		NextURL:             trailURL(state, nextDigest),
	}
	if inspector.ReferencesAvailable {
		inbound := s.cfg.References.Inbound(row.hash)
		inspector.InboundReferences = len(inbound)
		inspector.Inbound = s.referenceRows(ctx, state, inbound)
		inspector.Outbound = s.referenceRows(ctx, state, s.cfg.References.Outbound(row.hash))
	}
	return inspector
}

// renderObjects emits the part of the browser the request asked for: a cold
// load renders the whole page, while htmx asks for the inspector alone, the
// inspector plus the list refresh a selection triggers, or the list.
func (s *Server) renderObjects(w http.ResponseWriter, r *http.Request, data objectBrowserData) {
	if r.Header.Get("HX-Request") != "true" {
		s.renderPage(w, "objects", data)
		return
	}
	if r.Header.Get("HX-Target") == "object-inspector" {
		if r.Header.Get("X-Viewer-Selection") == "true" {
			s.render(w, "object-selection", data)
			return
		}
		s.render(w, "object-inspector", data)
		return
	}
	s.render(w, "object-list-swap", data) // htmx search/refresh swap
}

// typeOptions renders the type filter's choices.
func typeOptions(types []string, selected string) []filterOption {
	options := make([]filterOption, 0, len(types))
	for _, typ := range types {
		options = append(options, filterOption{Value: typ, Label: typ, Selected: typ == selected})
	}
	return options
}

// totalSize sums the matched rows, which the pager reports beside their count.
func totalSize(rows []objectRow) int64 {
	var total int64
	for _, row := range rows {
		total += row.Size
	}
	return total
}

func (s *Server) referenceRows(ctx context.Context, state objectBrowserState, digests []cas.Digest) []referenceRow {
	rows := make([]referenceRow, 0, len(digests))
	for _, digest := range digests {
		rowState := state
		rowState.Query = ""
		rowState.Type = ""
		rowState.Size = ""
		rowState.Status = ""
		rowState.Reach = ""
		rowState.Offset = 0
		rowState.OffsetSet = false
		rowState.Selected = digest.String()
		rowState.Deselected = false
		rowState.Nav = ""
		rows = append(rows, referenceRow{
			Digest:    digest.String(),
			Short:     shortDigest(digest),
			Type:      strings.TrimSuffix(s.objectMetaFor(ctx, digest).Type, "@1"),
			SelectURL: rowState.navURL(navReference),
		})
	}
	return rows
}

// trailURL selects a neighbouring trail entry. An empty digest yields an empty
// URL, which renders the control disabled rather than as a dead link.
func trailURL(state objectBrowserState, digest string) string {
	if digest == "" {
		return ""
	}
	state.Selected = digest
	state.Deselected = false
	// The trail entry may live on another page, so the jump that follows a
	// reference link applies here too.
	state.Offset = 0
	state.OffsetSet = false
	return state.navURL(navTrail)
}

// objectPageSizes lists the offered page sizes. A URL may still request any
// size up to maxObjectLimit, which limitOptions surfaces as an extra choice so
// the control never misreports the page it is showing.
var objectPageSizes = []int{25, 50, 100, maxObjectLimit}

// statusOptions renders the integrity filter's choices. Integrity states are
// alternatives on one axis, so exactly one of them can be selected.
func statusOptions(selected string) []filterOption {
	options := make([]filterOption, 0, len(objectStates))
	for _, state := range objectStates {
		options = append(options, filterOption{
			Value:    state,
			Label:    integrityLabel(state),
			Selected: state == selected,
		})
	}
	return options
}

// limitOptions renders the page-size control. A size the URL asked for but the
// control does not offer is inserted in order, so a clamped or hand-edited
// limit shows the page size actually in effect instead of silently reading as
// one of the presets.
func limitOptions(selected int) []filterOption {
	sizes := objectPageSizes
	if index, found := slices.BinarySearch(sizes, selected); !found {
		sizes = slices.Insert(slices.Clone(sizes), index, selected)
	}
	options := make([]filterOption, 0, len(sizes))
	for _, size := range sizes {
		label := strconv.Itoa(size)
		options = append(options, filterOption{
			Value:    label,
			Label:    label,
			Selected: size == selected,
		})
	}
	return options
}

// integrityLabel renders the byte-integrity axis. Reachability is a separate
// axis with its own column and filter, so Orphaned is never an integrity
// verdict and never appears here.
func integrityLabel(status string) string {
	switch status {
	case "not-verified":
		return "Unverified"
	case "verified":
		return "Verified"
	case "corrupt":
		return "Corrupt"
	default:
		return status
	}
}

func matchesObjectRow(row objectRow, state objectBrowserState) bool {
	if state.Query != "" && !strings.Contains(row.Digest, state.Query) && !strings.Contains(strings.ToLower(row.Type), state.Query) {
		return false
	}
	if state.Type != "" && row.Type != state.Type {
		return false
	}
	// Integrity and reachability are independent axes, so each narrows the
	// match on its own: a corrupt orphan needs both facts to match.
	if state.Status != "" && row.Integrity != state.Status {
		return false
	}
	switch state.Reach {
	case "orphaned":
		if !row.Orphaned {
			return false
		}
	case "reachable":
		if row.Orphaned {
			return false
		}
	}
	switch state.Size {
	case "small":
		return row.Size < 1<<10
	case "medium":
		return row.Size >= 1<<10 && row.Size <= 1<<20
	case "large":
		return row.Size > 1<<20
	default:
		return true
	}
}

func sortObjectRows(rows []objectRow, state objectBrowserState) {
	slices.SortFunc(rows, func(left, right objectRow) int {
		var comparison int
		switch state.Sort {
		case "type":
			comparison = strings.Compare(left.Type, right.Type)
		case "size":
			comparison = cmp.Compare(left.Size, right.Size)
		case "inbound":
			comparison = cmp.Compare(left.References, right.References)
		case "status":
			// Ascending puts the sound state first, matching the other axes,
			// so the key is ranked rather than compared as text: alphabetical
			// order would lead with "corrupt".
			comparison = cmp.Compare(integrityOrder(left.Integrity), integrityOrder(right.Integrity))
		case "reach":
			// Ascending puts the sound state first, matching the other axes.
			comparison = cmp.Compare(boolOrder(left.Orphaned), boolOrder(right.Orphaned))
		case "written":
			comparison = left.Written.Compare(right.Written)
		default:
			comparison = strings.Compare(left.Digest, right.Digest)
		}
		if comparison == 0 {
			comparison = strings.Compare(left.Digest, right.Digest)
		}
		if state.Direction == "desc" {
			return -comparison
		}
		return comparison
	})
}

// integrityOrder ranks an integrity key so an ascending sort reads from sound
// to suspect, the order the status filter lists them in. Comparing the keys as
// text would order them corrupt, not-verified, verified — backwards.
func integrityOrder(integrity string) int {
	switch integrity {
	case "verified":
		return 0
	case "corrupt":
		return 2
	default:
		return 1
	}
}

// boolOrder ranks a flag so the false state sorts first, which keeps an
// ascending sort on a two-state axis reading "sound before suspect" like the
// other columns.
func boolOrder(flag bool) int {
	if flag {
		return 1
	}
	return 0
}

func paginationURL(state objectBrowserState, offset int) string {
	state.Offset = offset
	// A pager link is an explicit page request, so it must survive even when it
	// lands on page 1 and even when the selection lives elsewhere.
	state.OffsetSet = true
	return state.url()
}

func sortURL(state objectBrowserState, key string) string {
	if state.Sort == key {
		if state.Direction == "asc" {
			state.Direction = "desc"
		} else {
			state.Direction = "asc"
		}
	} else {
		state.Sort = key
		state.Direction = "asc"
	}
	state.Offset = 0
	return state.url()
}

// --- object detail ---

// objectDetail is the cold-load entry point for a single object
// (viewer-design §3): a bookmark or a shared link. The viewer has exactly one
// object view — the browser's inspector — so this route selects the object
// there rather than rendering a second, divergent detail page.
func (s *Server) objectDetail(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigest(w, r)
	if !ok {
		return
	}
	if s.objectMetaFor(r.Context(), h).Unreadable {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	state := defaultObjectBrowserState()
	state.Selected = h.String()
	http.Redirect(w, r, state.url(), http.StatusSeeOther)
}

func (s *Server) objectRaw(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigest(w, r)
	if !ok {
		return
	}
	data, truncated, err := s.readPreview(r.Context(), h)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("preview truncated at %s of %s", formatBytes(previewLimit), formatBytes(s.objectMetaFor(r.Context(), h).Size))
	}
	s.render(w, "hexdump-table", struct {
		Rows []dumpRow
		Note string
	}{hexdump(data), note})
}

// verifyAllFragment verifies every stored object and records each result in the
// session. It is the bulk counterpart of verifyFragment: one audit line per
// object would flood the log, so it audits the sweep as a single event with
// counts.
func (s *Server) verifyAllFragment(w http.ResponseWriter, r *http.Request) {
	digests, err := s.store.List(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	id := sessionID(r)
	hasher := sha256.New()
	verified, corrupt := 0, 0
	for _, h := range digests {
		if err := r.Context().Err(); err != nil {
			return
		}
		if err := s.store.Verify(r.Context(), h, hasher); err != nil {
			outcome := s.describeVerifyFailure(r.Context(), h, err)
			outcome.Integrity = "corrupt"
			outcome.IntegrityLabel = integrityLabel("corrupt")
			s.sessions.setVerification(id, h.String(), "corrupt", outcome)
			corrupt++
			continue
		}
		s.sessions.setVerification(id, h.String(), "verified", verifiedOutcome(h))
		verified++
	}
	slog.Info("viewer audit", "action", "object.verify-all", "session", sessionHandle(id), "objects", len(digests), "verified", verified, "corrupt", corrupt)
	w.Header().Set("HX-Trigger", "object-status-updated")
	// The label stays "Verify": the per-object status cells already carry the
	// outcome, so a count on the control would only duplicate them.
	s.render(w, "verify-all-button", verifyAllState{
		CSRF:  s.csrfFor(r),
		Label: "Verify",
	})
}

// verifyAllState backs the top-bar Verify control.
type verifyAllState struct {
	CSRF  string
	Label string
}

// actionOutcome is the structured result of an object action (verification).
// It replaces the raw error string: a sentinel classifies the failure and the
// recomputed digest shows the operator exactly how the bytes diverged.
type actionOutcome struct {
	OK       bool
	Headline string
	Summary  string
	Expected string
	Actual   string
	Detail   string
	Checked  string
	// Integrity and IntegrityLabel refresh the inspector's Integrity row out
	// of band; they stay empty for actions that leave no object behind.
	Integrity      string
	IntegrityLabel string
}

func (s *Server) verifyFragment(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigest(w, r)
	if !ok {
		return
	}
	// Every operator action is audit-logged with the acting session, the
	// affected object, and the result (viewer-security §9).
	id := sessionID(r)
	if err := s.store.Verify(r.Context(), h, sha256.New()); err != nil {
		slog.Info("viewer audit", "action", "object.verify", "session", sessionHandle(id), "hash", h, "valid", false)
		w.Header().Set("HX-Trigger", "object-status-updated")
		outcome := s.describeVerifyFailure(r.Context(), h, err)
		outcome.Integrity = "corrupt"
		outcome.IntegrityLabel = integrityLabel("corrupt")
		s.sessions.setVerification(id, h.String(), "corrupt", outcome)
		outcome.Checked = checkedLabel(s.sessions, id, h.String())
		s.render(w, "result-swap", outcome)
		return
	}
	slog.Info("viewer audit", "action", "object.verify", "session", sessionHandle(id), "hash", h, "valid", true)
	w.Header().Set("HX-Trigger", "object-status-updated")
	outcome := verifiedOutcome(h)
	// The report is stored before the check time is stamped onto it: the label
	// is relative ("3m ago"), so it has to be derived per render rather than
	// frozen at the moment of the check.
	s.sessions.setVerification(id, h.String(), "verified", outcome)
	outcome.Checked = checkedLabel(s.sessions, id, h.String())
	s.render(w, "result-swap", outcome)
}

// verifiedOutcome is the report of a successful check. The sweep and the
// per-object action share it so a swept object and a clicked one describe
// themselves identically.
func verifiedOutcome(h cas.Digest) actionOutcome {
	return actionOutcome{
		OK:             true,
		Headline:       "Verified",
		Summary:        "Stored bytes hash to this address.",
		Expected:       h.String(),
		Integrity:      "verified",
		IntegrityLabel: integrityLabel("verified"),
	}
}

// describeVerifyFailure turns a verification error into operator-facing prose.
func (s *Server) describeVerifyFailure(ctx context.Context, h cas.Digest, err error) actionOutcome {
	switch {
	case errors.Is(err, cas.ErrDigestMismatch):
		return actionOutcome{
			Headline: "Corrupt",
			Summary:  "Stored bytes no longer hash to this address, so the content has changed since it was written. The object is unusable and must be restored from a backup or re-ingested.",
			Expected: h.String(),
			Actual:   s.recomputeDigest(ctx, h),
		}
	case errors.Is(err, cas.ErrNotFound):
		return actionOutcome{
			Headline: "Missing",
			Summary:  "The object is no longer present in the store.",
			Expected: h.String(),
		}
	default:
		return actionOutcome{
			Headline: "Unreadable",
			Summary:  "The object could not be read for verification, so its integrity is unknown.",
			Expected: h.String(),
			Detail:   err.Error(),
		}
	}
}

// recomputeDigest reports the digest the stored bytes actually hash to, so a
// mismatch shows both sides rather than one unexplained number. It returns an
// empty string when the bytes cannot be re-read.
func (s *Server) recomputeDigest(ctx context.Context, h cas.Digest) string {
	rc, err := s.store.Get(ctx, h)
	if err != nil {
		return ""
	}
	defer rc.Close()
	actual, err := sha256.New().Digest(rc)
	if err != nil {
		return ""
	}
	return actual.String()
}

func (s *Server) htmx(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	if _, err := w.Write(htmxJS); err != nil {
		slog.Error("viewer htmx write", "err", err)
	}
}

func (s *Server) css(w http.ResponseWriter, r *http.Request) {
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
	View string
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

// typePrefixLimit bounds the bytes read for envelope type sniffing: only the
// TLV header ([version][uvarint typeLen][type]) is needed, not the payload.
const typePrefixLimit = 4 << 10

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

func (s *Server) csrfFor(r *http.Request) string {
	if sess, ok := s.sessions.get(sessionID(r)); ok {
		return sess.CSRF
	}
	return ""
}

func (s *Server) roleFor(r *http.Request) string {
	if sess, ok := s.sessions.get(sessionID(r)); ok {
		return sess.Role
	}
	return ""
}

func parseDigest(w http.ResponseWriter, r *http.Request) (cas.Digest, bool) {
	d, err := sha256.Parse(r.PathValue("hash"))
	if err != nil {
		http.Error(w, "malformed hash", http.StatusBadRequest)
		return nil, false
	}
	return d, true
}

// hexdump renders a classic 16-byte-row dump (offset, hex, ASCII).
func hexdump(data []byte) []dumpRow {
	var rows []dumpRow
	for off := 0; off < len(data); off += 16 {
		end := min(off+16, len(data))
		var hexParts []string
		var ascii strings.Builder
		for _, b := range data[off:end] {
			hexParts = append(hexParts, fmt.Sprintf("%02x", b))
			if b >= 32 && b < 127 {
				ascii.WriteByte(b)
			} else {
				ascii.WriteByte('.')
			}
		}
		rows = append(rows, dumpRow{fmt.Sprintf("%08x", off), strings.Join(hexParts, " "), ascii.String()})
	}
	return rows
}

type dumpRow struct {
	// Offset is the hexadecimal byte offset.
	Offset string
	// Hex is the formatted byte sequence.
	Hex string
	// ASCII is the printable representation.
	ASCII string
}

func callerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
