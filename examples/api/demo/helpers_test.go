package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// This file closes the demo's request-building and response-reading helpers:
// mustReq's request and bearer header and doRaw's strict status check are the
// two places the demo turns a server's answer into what it prints.
//
// Deliberately left uncovered, with the reason:
//
//   - main (main.go:23): the runnable program — it parses flags, opens a file
//     and talks to a live server.
//   - the fatal exits inside mustReq and doRaw (main.go:88, 140, 144, 148) and
//     fatal itself (main.go:160): each writes to stderr and calls os.Exit, so
//     only a subprocess could observe it — that tests the exit plumbing rather
//     than the helper's behaviour.

// mustReq builds a GET/POST request and attaches the bearer token; the caller
// (main) relies on the header being set, not on doing it itself.
func TestMustReqBuildsAuthorizedRequest(t *testing.T) {
	const token = "operator-tok"
	req := mustReq(context.Background(), http.MethodPost, "http://127.0.0.1:8080/api/cas/v1/objects", token)

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "http://127.0.0.1:8080/api/cas/v1/objects" {
		t.Fatalf("url = %q, want the requested URL", req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer "+token)
	}
}

// num returns the value of a JSON number field and 0 for anything else: main
// prints it directly, so an absent or non-numeric field must not panic.
func TestNum(t *testing.T) {
	cases := []struct {
		name string
		m    map[string]any
		key  string
		want float64
	}{
		{name: "JSON number", m: map[string]any{"size": float64(7)}, key: "size", want: 7},
		{name: "zero-value number", m: map[string]any{"size": float64(0)}, key: "size", want: 0},
		{name: "absent field", m: map[string]any{}, key: "size", want: 0},
		{name: "string value", m: map[string]any{"size": "7"}, key: "size", want: 0},
		{name: "bool value", m: map[string]any{"size": true}, key: "size", want: 0},
		{name: "nil value", m: map[string]any{"size": nil}, key: "size", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := num(tc.m, tc.key); got != tc.want {
				t.Fatalf("num(%v, %q) = %v, want %v", tc.m, tc.key, got, tc.want)
			}
		})
	}
}

// num on a value json.Unmarshal would produce for a large integer is still a
// float64; the helper never rounds or truncates.
func TestNumReturnsDecodedNumbers(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(`{"total_size":1234567890}`), &m); err != nil {
		t.Fatal(err)
	}
	if got := num(m, "total_size"); got != 1234567890 {
		t.Fatalf("num(total_size) = %v, want 1234567890", got)
	}
}

// mustString and mustBool name the offending field and its actual type when the
// server's answer does not have the shape the demo expects.
func TestMustStringAndBoolRejectWrongTypes(t *testing.T) {
	cases := []struct {
		name    string
		m       map[string]any
		wantMsg string
	}{
		{name: "string field holds a number", m: map[string]any{"hash": float64(1)}, wantMsg: `field "hash" is float64, want string`},
		{name: "string field holds a bool", m: map[string]any{"hash": true}, wantMsg: `field "hash" is bool, want string`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mustString(tc.m, "hash")
			if err == nil {
				t.Fatal("mustString accepted a non-string field")
			}
			if err.Error() != tc.wantMsg {
				t.Fatalf("mustString error = %q, want %q", err, tc.wantMsg)
			}
		})
	}

	_, err := mustBool(map[string]any{"deduplicated": "true"}, "deduplicated")
	if err == nil {
		t.Fatal("mustBool accepted a non-bool field")
	}
	if err.Error() != `field "deduplicated" is string, want bool` {
		t.Fatalf("mustBool error = %q, want the field and its type", err)
	}

	// A present-but-nil field is a type error too, never a silent false.
	if _, err := mustBool(map[string]any{"deduplicated": nil}, "deduplicated"); err == nil {
		t.Fatal("mustBool accepted a nil field")
	}
}

// doJSON prefixes every failure with the request's name, so the demo's error
// says which call failed.
func TestDoJSONErrorNamesTheRequest(t *testing.T) {
	t.Run("non-2xx status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusTeapot)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = doJSON(req, "meta")
		if err == nil {
			t.Fatal("doJSON accepted a 418 response")
		}
		if !strings.HasPrefix(err.Error(), "meta: status 418") {
			t.Fatalf("doJSON error = %q, want it to name the request and the status", err)
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Fatalf("doJSON error = %q, want the response body echoed", err)
		}
	})

	t.Run("body that is not a JSON object", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json at all"))
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = doJSON(req, "stats")
		if err == nil {
			t.Fatal("doJSON accepted a non-JSON body")
		}
		if !strings.Contains(err.Error(), "stats: decode:") {
			t.Fatalf("doJSON error = %q, want it to name the decode step", err)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = doJSON(req, "put")
		if err == nil {
			t.Fatal("doJSON accepted an empty body")
		}
		if !strings.Contains(err.Error(), "put: decode:") {
			t.Fatalf("doJSON error = %q, want it to name the decode step", err)
		}
	})
}

// doJSON against an unreachable server reports the transport error unchanged.
func TestDoJSONTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close() // nothing is listening on the address now

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doJSON(req, "stats"); err == nil {
		t.Fatal("doJSON succeeded against a closed server")
	}
}

// A JSON number beyond float64's exact range still comes back through num as
// the decoded float, never a panic.
func TestNumHandlesExtremeValues(t *testing.T) {
	m := map[string]any{"total_size": math.MaxFloat64}
	if got := num(m, "total_size"); got != math.MaxFloat64 {
		t.Fatalf("num(MaxFloat64) = %v, want MaxFloat64", got)
	}
}

// doRaw streams the whole response body back to the caller.
func TestDoRawReadsBody(t *testing.T) {
	const body = "the stored bytes, streamed back"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer op-tok" {
			t.Errorf("Authorization = %q, want the caller's bearer token", got)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	got := doRaw(mustReq(context.Background(), http.MethodGet, server.URL+"/api/cas/v1/objects/abc", "op-tok"))
	if string(got) != body {
		t.Fatalf("doRaw = %q, want %q", got, body)
	}
}

// doRaw rejects a non-200 answer rather than returning its error body as if it
// were the object: fatal exits the process, which the child-process test below
// observes.
func TestDoRawRejectsNon200(t *testing.T) {
	if os.Getenv("DEMO_DORAW_HELPER") == "non-200" {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer server.Close()
		doRaw(mustReq(context.Background(), http.MethodGet, server.URL, "tok"))
		return // unreachable: doRaw must have exited
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestDoRawRejectsNon200")
	cmd.Env = append(os.Environ(), "DEMO_DORAW_HELPER=non-200")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doRaw on a 404 returned instead of failing: %s", out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("doRaw exit = %v (output %s), want exit status 1", err, out)
	}
	if !strings.Contains(string(out), "get: status 404") {
		t.Fatalf("doRaw output = %q, want it to name the status", out)
	}
}
