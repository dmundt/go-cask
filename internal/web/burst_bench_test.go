package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// benchmarkBurst is how many concurrent verify-all requests the burst benchmark
// fires per iteration: the shape #181 bounds, where eight authenticated
// refreshes used to multiply one store-wide re-read and re-hash.
const benchmarkBurst = 8

// benchmarkStoreSize is the object count the burst benchmark seeds. Each
// verify-all re-reads and re-hashes every one of them, so the burst's cost is
// proportional to this.
const benchmarkStoreSize = 500

// BenchmarkVerifyAllBurst measures the cost of a burst of concurrent verify-all
// requests over a synthetic store, and how many of them were refused.
//
// With the bound (internal/web/limit.go) one sweep runs and the rest answer 429
// immediately, so ns/op stays close to a single sweep; without it every request
// in the burst walks and re-hashes the store. #181's numbers were measured by
// running this benchmark with and without the limiter.
func BenchmarkVerifyAllBurst(b *testing.B) {
	ctx := context.Background()
	backend, err := fs.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	for i := range benchmarkStoreSize {
		frame := test.TLVEnvelope("blob@1", []byte(fmt.Sprintf("object %d", i)))
		digest := sha256.Of(frame)
		if err := backend.Put(ctx, digest, bytes.NewReader(frame)); err != nil {
			b.Fatal(err)
		}
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		b.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	b.Cleanup(ts.Close)
	client := login(b, ts, testStartupToken)
	csrf := csrfFromPage(getBody(b, client, ts.URL+"/viewer/objects"))

	var refused int64
	b.ResetTimer()
	for range b.N {
		var wg sync.WaitGroup
		for range benchmarkBurst {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
				if err != nil {
					return
				}
				if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt64(&refused, 1)
				}
				resp.Body.Close()
			}()
		}
		wg.Wait()
		// The per-session bucket needs its cooldown to hand the next iteration
		// a token; without it the measured loop would be all refusals after the
		// first iteration instead of one sweep per iteration.
		time.Sleep(10 * time.Millisecond)
	}
	b.StopTimer()
	b.ReportMetric(float64(refused)/float64(b.N), "refusals/op")
}
