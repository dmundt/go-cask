package benchmark_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/web"
)

// BenchmarkViewerObjectsScale measures authenticated object-browser rendering
// over a filesystem store at four realistic metadata-scan sizes. The scale
// gate is shared with the other opt-in probes: CASK_SCALE_OBJECTS is the
// largest case to run, so 100000 runs every documented case.
//
//	CASK_SCALE_OBJECTS=100000 go test ./benchmarks/ -run=^$ \
//	  -bench='^BenchmarkViewerObjectsScale$' -benchmem -benchtime=10x -timeout 0
//
// Prefill time is excluded. The timed path uses the viewer's snapshot cache,
// which is the normal request flow after the first page load.
func BenchmarkViewerObjectsScale(b *testing.B) {
	maxObjects := scaleObjectCount(b)
	for _, count := range viewerScaleCases {
		if count > maxObjects {
			continue
		}
		b.Run(fmt.Sprintf("objects=%d", count), func(b *testing.B) {
			backend, err := fs.New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			fillViewerObjects(b, backend, count)

			server, err := web.New(backend, web.Config{
				RoleTokens: map[string]string{"benchmark-token": web.RoleViewer},
			})
			if err != nil {
				b.Fatal(err)
			}
			handler := server.Handler()
			cookie := viewerBenchmarkSession(b, handler)
			assertViewerObjects(b, handler, cookie)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				assertViewerObjects(b, handler, cookie)
			}
		})
	}
}

var viewerScaleCases = [...]int{100, 1_000, 10_000, 100_000}

func fillViewerObjects(b *testing.B, backend *fs.Backend, count int) {
	b.Helper()
	ctx := context.Background()
	payload := make([]byte, 64)
	for i := range count {
		binary.BigEndian.PutUint64(payload, uint64(i))
		for j := 8; j < len(payload); j++ {
			payload[j] = byte(i*(j+1) + j)
		}
		object := viewerEnvelope(payload)
		if err := backend.Put(ctx, sha256.Of(object), bytes.NewReader(object)); err != nil {
			b.Fatal(err)
		}
	}
}

func viewerEnvelope(payload []byte) []byte {
	const typ = "note@1"
	object := make([]byte, 0, 1+1+len(typ)+1+len(payload))
	object = append(object, 1, byte(len(typ)))
	object = append(object, typ...)
	object = append(object, byte(len(payload)))
	return append(object, payload...)
}

func viewerBenchmarkSession(b *testing.B, handler http.Handler) *http.Cookie {
	b.Helper()
	request := httptest.NewRequest(http.MethodGet, "/viewer/?token=benchmark-token", nil)
	// The documented deep link is a top-level navigation the viewer accepts
	// only from its own origin (viewer-security §5.1).
	request.Header.Set("Sec-Fetch-Site", "none")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		b.Fatalf("viewer token login = %d, want %d", response.Code, http.StatusSeeOther)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		b.Fatalf("viewer token login cookies = %d, want 1", len(cookies))
	}
	return cookies[0]
}

func assertViewerObjects(b *testing.B, handler http.Handler, cookie *http.Cookie) {
	b.Helper()
	request := httptest.NewRequest(http.MethodGet, "/viewer/objects", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		b.Fatalf("viewer objects = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.Len() == 0 {
		b.Fatal("viewer objects response is empty")
	}
}
