package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMustStringAndBool(t *testing.T) {
	m := map[string]any{"hash": "abc123", "deduplicated": true}
	if got, err := mustString(m, "hash"); err != nil || got != "abc123" {
		t.Fatalf("mustString() = (%q, %v), want (abc123, nil)", got, err)
	}
	if got, err := mustBool(m, "deduplicated"); err != nil || !got {
		t.Fatalf("mustBool() = (%v, %v), want (true, nil)", got, err)
	}
	if _, err := mustString(m, "missing"); err == nil {
		t.Fatal("mustString(missing) = nil, want error")
	}
	if _, err := mustBool(m, "missing"); err == nil {
		t.Fatal("mustBool(missing) = nil, want error")
	}
}

func TestDoJSONValidatesResponseBody(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"hash": "deadbeef", "deduplicated": true})
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		m, err := doJSON(req, "test")
		if err != nil {
			t.Fatalf("doJSON() = %v, want nil", err)
		}
		if got, err := mustString(m, "hash"); err != nil || got != "deadbeef" {
			t.Fatalf("hash = (%q, %v), want (deadbeef, nil)", got, err)
		}
	})

	t.Run("non-2xx errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadRequest)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := doJSON(req, "test"); err == nil {
			t.Fatal("doJSON() = nil, want error")
		}
	})
}
