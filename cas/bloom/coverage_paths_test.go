package bloom_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
	stdfilter "github.com/dmundt/go-cask/cas/bloom/standard"
)

// failingBackend fails every write, so the guard's pass-through branch is
// reached without a real backend that fails on demand.
type failingBackend struct {
	inner cas.Backend
	put   error
}

func (b *failingBackend) Put(context.Context, cas.Digest, io.Reader) error { return b.put }
func (b *failingBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}
func (b *failingBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}
func (b *failingBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}
func (b *failingBackend) List(ctx context.Context) ([]cas.Digest, error) { return b.inner.List(ctx) }
func (b *failingBackend) Stats(ctx context.Context) (*cas.Stats, error)  { return b.inner.Stats(ctx) }

// TestGuardPutReportsTheBackendFailureWithoutRecordingTheDigest pins the
// guard's error branch: a write the backend refused is returned unchanged, and
// the digest is NOT added to the advisory filter — recording a digest that was
// never stored would make the filter claim presence for an absent object.
func TestGuardPutReportsTheBackendFailureWithoutRecordingTheDigest(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("put exploded")
	filter, err := stdfilter.New(256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := bloom.NewGuard(&failingBackend{inner: backmem.New(), put: boom}, filter)
	if err != nil {
		t.Fatal(err)
	}

	d := cas.NewDigest([]byte("never written"))
	err = guard.Put(ctx, d, bytes.NewReader([]byte("payload")))
	if !errors.Is(err, boom) {
		t.Fatalf("Put through a failing backend = %v, want %v", err, boom)
	}
	if filter.Contains(d) {
		t.Fatal("a rejected write recorded the digest in the filter")
	}
	if ok, err := guard.Exists(ctx, d); err != nil || ok {
		t.Fatalf("Exists after the rejected write = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestParametersRejectsAnUnusableBitCount pins the invalid-bit-count branch's
// reachability boundary with the inputs that reach the adjacent branches: the
// documented rejection a caller with an implausible request actually sees is
// the ceiling error (bits above MaxBits), because the false-positive-rate guard
// above it already restricts the rate to the open interval (0, 1) — with
// expectedItems > 0 and such a rate, -n*ln(rate)/ln(2)^2 is always a positive
// finite number, so the NaN/Inf/non-positive guard is defensive rather than
// reachable from the exported surface.
func TestParametersRejectsAnUnusableBitCount(t *testing.T) {
	if _, _, err := bloom.Parameters(0, 0.01); err != nil {
		t.Fatalf("Parameters(0 items) = %v, want the minimal (1, 1) filter", err)
	}
	for _, tc := range []struct {
		name          string
		expectedItems uint64
		rate          float64
	}{
		{"an absurd item count", uint64(1) << 62, 0.5},
		{"a denormal false-positive rate", uint64(1) << 62, 5e-324},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, k, err := bloom.Parameters(tc.expectedItems, tc.rate)
			if err == nil {
				t.Fatalf("Parameters(%d items, rate %g) = (%d, %d), want the ceiling error", tc.expectedItems, tc.rate, m, k)
			}
			if !strings.Contains(err.Error(), "above the") {
				t.Fatalf("Parameters(%d items, rate %g) = %v, want the MaxBits ceiling error; the NaN/Inf guard above it is defensive and must not be what fires", tc.expectedItems, tc.rate, err)
			}
		})
	}
}
