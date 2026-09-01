// Package client implements the public CAS API client SDK: typed access to
// a remote CASK server's `/api/cas/v1` surface (cas-api.instructions.md).
// It mirrors the HTTP contract exactly — content-addressed store, list,
// meta, verify, gc, stats, OpenAPI — with streaming uploads/downloads and
// bearer-token auth. It is the remote twin of the embedded `cas` library:
// programs can use either over the same object model.
//
// Errors: HTTP error responses are surfaced as wrapped sentinel errors —
// 401 → ErrUnauthorized, 403 → ErrForbidden, 404 → ErrNotFound (wrapping
// cas.ErrNotFound), 429 → ErrRateLimited — with the server's message.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Sentinel errors for the remote surface.
var (
	ErrUnauthorized = errors.New("client: unauthorized")
	ErrForbidden    = errors.New("client: forbidden")
	ErrRateLimited  = errors.New("client: rate limited")
)

// apiPrefix is the versioned CAS API prefix.
const apiPrefix = "/api/cas/v1"

// Client is a typed client for the CAS API. It is safe for concurrent use.
type Client struct {
	baseURL string // server root, e.g. http://localhost:8080
	token   string // bearer token (any role)
	hc      *http.Client
}

// New creates a client for the server at baseURL (scheme://host[:port]),
// authenticating with token. All calls target <baseURL>/api/cas/v1/... .
func New(baseURL, token string) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), token: token, hc: &http.Client{}}
}

// url builds the full request URL for a path under the API prefix.
func (c *Client) url(path string) *url.URL {
	u, _ := url.Parse(c.baseURL + apiPrefix + path)
	return u
}

// Meta is object metadata as returned by the API.
type Meta struct {
	Hash       cas.Hash `json:"hash"`
	Algorithm  string   `json:"algorithm"`
	Size       int64    `json:"size"`
	Type       string   `json:"type,omitempty"`
	References []string `json:"references,omitempty"`
}

// UnmarshalJSON parses the string hash form.
func (m *Meta) UnmarshalJSON(data []byte) error {
	var raw struct {
		Hash       string   `json:"hash"`
		Algorithm  string   `json:"algorithm"`
		Size       int64    `json:"size"`
		Type       string   `json:"type"`
		References []string `json:"references"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	h, err := cas.ParseHash(raw.Hash)
	if err != nil {
		return fmt.Errorf("client: invalid hash in metadata: %w", err)
	}
	m.Hash, m.Algorithm, m.Size, m.Type, m.References = h, raw.Algorithm, raw.Size, raw.Type, raw.References
	return nil
}

// ListOptions controls GET /objects pagination and filtering.
type ListOptions struct {
	Algo   string // filter by algorithm ("" = all)
	Limit  int    // 1–1000, default 100
	Offset int    // >= 0, default 0
}

// ListResult is the GET /objects response shape.
type ListResult struct {
	Total   int64  `json:"total"`
	Objects []Meta `json:"objects"`
}

// Stats is the GET /stats response shape.
type Stats struct {
	ObjectCount     int64            `json:"object_count"`
	TotalSize       int64            `json:"total_size"`
	AlgorithmCounts map[string]int64 `json:"algorithm_counts"`
}

// VerifyResult is the POST /objects/{hash}/verify response shape.
type VerifyResult struct {
	Hash       cas.Hash `json:"hash"`
	Valid      bool     `json:"valid"`
	Recomputed string   `json:"recomputed"`
}

// UnmarshalJSON parses the string hash form.
func (v *VerifyResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Hash       string `json:"hash"`
		Valid      bool   `json:"valid"`
		Recomputed string `json:"recomputed"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	h, err := cas.ParseHash(raw.Hash)
	if err != nil {
		return fmt.Errorf("client: invalid hash in verify result: %w", err)
	}
	v.Hash, v.Valid, v.Recomputed = h, raw.Valid, raw.Recomputed
	return nil
}

// Put stores the bytes read from r under the hash computed with algo
// (default "sha256"; any registered algorithm). It returns the content
// address and whether the object already existed (deduplicated). The body
// is streamed from r — never buffered by the client.
func (c *Client) Put(ctx context.Context, r io.Reader, algo string) (cas.Hash, bool, error) {
	if algo == "" {
		algo = "sha256"
	}
	u := c.url("/objects")
	q := u.Query()
	q.Set("algo", algo)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), r)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	var resp struct {
		Hash         string `json:"hash"`
		Deduplicated bool   `json:"deduplicated"`
	}
	if err := c.doJSON(req, http.StatusCreated, &resp); err != nil {
		return nil, false, err
	}
	h, err := cas.ParseHash(resp.Hash)
	if err != nil {
		return nil, false, fmt.Errorf("client: server returned invalid hash %q: %w", resp.Hash, err)
	}
	return h, resp.Deduplicated, nil
}

// Get streams the object's bytes; the caller MUST close the returned
// ReadCloser. Metadata from the X-CAS-* headers is returned in meta.
func (c *Client) Get(ctx context.Context, h cas.Hash) (io.ReadCloser, *Meta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/objects/"+h.String()).String(), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, nil, c.statusError(resp)
	}
	meta := &Meta{Hash: h, Algorithm: resp.Header.Get("X-CAS-Algorithm")}
	if s := resp.Header.Get("X-CAS-Size"); s != "" {
		meta.Size, _ = strconv.ParseInt(s, 10, 64)
	}
	meta.Type = resp.Header.Get("X-CAS-Type")
	return resp.Body, meta, nil
}

// GetBytes is a convenience that buffers the whole object (use Get for
// streaming).
func (c *Client) GetBytes(ctx context.Context, h cas.Hash) ([]byte, error) {
	rc, _, err := c.Get(ctx, h)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Delete removes the object (admin role). Deleting a missing object is a
// no-op on the server.
func (c *Client) Delete(ctx context.Context, h cas.Hash) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url("/objects/"+h.String()).String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return c.statusError(resp)
	}
	return nil
}

// Meta retrieves the object's metadata and references.
func (c *Client) Meta(ctx context.Context, h cas.Hash) (*Meta, error) {
	var m Meta
	if err := c.doJSON(reqGET(ctx, c.url("/objects/"+h.String()+"/meta")), http.StatusOK, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// List lists stored objects with optional algorithm filter and pagination.
func (c *Client) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	u := c.url("/objects")
	q := u.Query()
	if opts.Algo != "" {
		q.Set("algo", opts.Algo)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Offset > 0 {
		q.Set("offset", strconv.Itoa(opts.Offset))
	}
	u.RawQuery = q.Encode()
	var res ListResult
	if err := c.doJSON(reqGET(ctx, u), http.StatusOK, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Verify asks the server to recompute the object's hash (operator role).
func (c *Client) Verify(ctx context.Context, h cas.Hash) (*VerifyResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/objects/"+h.String()+"/verify").String(), nil)
	if err != nil {
		return nil, err
	}
	var v VerifyResult
	if err := c.doJSON(req, http.StatusOK, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Stats retrieves storage statistics.
func (c *Client) Stats(ctx context.Context) (*Stats, error) {
	var s Stats
	if err := c.doJSON(reqGET(ctx, c.url("/stats")), http.StatusOK, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GC runs mark-and-sweep on the server (admin role): every object whose
// hash is not in reachable is deleted. It returns the number deleted.
func (c *Client) GC(ctx context.Context, reachable []cas.Hash) (int64, error) {
	hashes := make([]string, 0, len(reachable))
	for _, h := range reachable {
		hashes = append(hashes, h.String())
	}
	body, err := json.Marshal(map[string][]string{"reachable": hashes})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/gc").String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	var res struct {
		Deleted int64 `json:"deleted"`
	}
	if err := c.doJSON(req, http.StatusOK, &res); err != nil {
		return 0, err
	}
	return res.Deleted, nil
}

// OpenAPI fetches the server's CAS API OpenAPI document (text/yaml).
func (c *Client) OpenAPI(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/openapi.yaml").String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(resp)
	}
	return io.ReadAll(resp.Body)
}

func reqGET(ctx context.Context, u *url.URL) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	return req
}

// do sends an authenticated request and returns the response; the caller
// MUST close resp.Body.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}
	return resp, nil
}

// doJSON sends an authenticated request expecting a JSON body with the
// given success status.
func (c *Client) doJSON(req *http.Request, wantStatus int, v any) error {
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		return c.statusError(resp)
	}
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return fmt.Errorf("client: decode response: %w", err)
		}
	}
	return nil
}

// statusError maps a non-success response to a wrapped sentinel error with
// the server's message.
func (c *Client) statusError(resp *http.Response) error {
	msg := ""
	if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil {
			msg = e.Error
		} else {
			msg = strings.TrimSpace(string(body))
		}
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, msg)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", cas.ErrNotFound, msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, msg)
	default:
		return fmt.Errorf("client: %s %s: %s (status %d)", resp.Request.Method, resp.Request.URL.Path, msg, resp.StatusCode)
	}
}
