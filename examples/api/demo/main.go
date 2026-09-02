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
	m := doJSON(req, "put")
	h := m["hash"].(string)
	dedup := m["deduplicated"].(bool)

	// GET /objects/{hash} — stream back.
	resp, err := http.NewRequestWithContext(ctx, http.MethodGet, *api+"/api/cas/v1/objects/"+h, nil)
	if err != nil {
		fatal(err)
	}
	resp.Header.Set("Authorization", "Bearer "+*token)
	got := doRaw(resp)
	fmt.Printf("fetched %d bytes\n", len(got))

	// GET meta + stats.
	meta := doJSON(mustReq(ctx, http.MethodGet, *api+"/api/cas/v1/objects/"+h+"/meta", *token), "meta")
	stats := doJSON(mustReq(ctx, http.MethodGet, *api+"/api/cas/v1/stats", *token), "stats")
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

// doJSON executes a request and decodes the JSON response, extracting the
// hash field for PUT and printing the response for the others.
func doJSON(req *http.Request, what string) map[string]any {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(resp.Body)
		fatal(fmt.Errorf("%s: status %d: %s", what, resp.StatusCode, strings.TrimSpace(string(b))))
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		fatal(fmt.Errorf("%s: decode: %w", what, err))
	}
	return m
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
