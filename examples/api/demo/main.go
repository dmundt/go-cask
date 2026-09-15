// Command api-demo round-trips a file through a running HTTP CAS server
// using plain net/http (the product ships no SDK — this is the pattern an
// app author without an SDK would follow): streaming upload, download,
// dedup, and metadata.
//
// Usage:
//
//	go run ./examples/api/demo -api http://127.0.0.1:8080 \
//	    -token operator -file ./data.txt
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	var (
		api   = flag.String("api", "http://127.0.0.1:8080", "server URL (the examples/api pattern)")
		token = flag.String("token", "operator", "bearer token")
		file  = flag.String("file", "", "file to store and fetch")
	)
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: api-demo -api <url> -token <tok> -file <path>")
		os.Exit(2)
	}

	ctx := context.Background()

	f, err := os.Open(*file)
	if err != nil {
		fatal(err)
	}
	defer f.Close()

	// POST /objects — the server computes the hash while streaming.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *api+"/api/cas/v1/objects", f)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/octet-stream")
	m, err := doJSON(req, "put")
	if err != nil {
		fatal(err)
	}
	h, err := mustString(m, "hash")
	if err != nil {
		fatal(fmt.Errorf("put response: %w", err))
	}
	dedup, err := mustBool(m, "deduplicated")
	if err != nil {
		fatal(fmt.Errorf("put response: %w", err))
	}

	// GET /objects/{hash} — stream back.
	resp, err := http.NewRequestWithContext(ctx, http.MethodGet, *api+"/api/cas/v1/objects/"+h, nil)
	if err != nil {
		fatal(err)
	}
	resp.Header.Set("Authorization", "Bearer "+*token)
	got := doRaw(resp)
	fmt.Printf("fetched %d bytes\n", len(got))

	// GET meta + stats.
	meta, err := doJSON(mustReq(ctx, http.MethodGet, *api+"/api/cas/v1/objects/"+h+"/meta", *token), "meta")
	if err != nil {
		fatal(err)
	}
	stats, err := doJSON(mustReq(ctx, http.MethodGet, *api+"/api/cas/v1/stats", *token), "stats")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("stored %s deduplicated=%v size=%v\n", h, dedup, num(meta, "size"))
	fmt.Printf("stats: %v objects, %v bytes\n", num(stats, "object_count"), num(stats, "total_size"))
}

func mustReq(ctx context.Context, method, url, token string) *http.Request {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// doJSON executes a request and decodes the JSON response, returning a map for
// the caller to validate instead of assuming a specific layout.
func doJSON(req *http.Request, what string) (map[string]any, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: status %d: %s", what, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", what, err)
	}
	return m, nil
}

func mustString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing %q field", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q is %T, want string", key, v)
	}
	return s, nil
}

func mustBool(m map[string]any, key string) (bool, error) {
	v, ok := m[key]
	if !ok {
		return false, fmt.Errorf("missing %q field", key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q is %T, want bool", key, v)
	}
	return b, nil
}

func doRaw(req *http.Request) []byte {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("get: status %d", resp.StatusCode))
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		fatal(err)
	}
	return b
}

func num(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
