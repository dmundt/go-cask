package bloom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestDefaultIndexHashIsStableAndChangesWithProbe(t *testing.T) {
	data := []byte("bitset-key")
	first := DefaultIndexHash(data, 0)
	second := DefaultIndexHash(data, 0)
	if first != second {
		t.Fatalf("DefaultIndexHash called twice with same input should be stable: got %d and %d", first, second)
	}

	probe1 := DefaultIndexHash(data, 0)
	probe2 := DefaultIndexHash(data, 1)
	if probe1 == probe2 {
		t.Fatal("DefaultIndexHash with different probe indexes should not produce the same value for a non-empty input")
	}
}

func TestParametersHandlesEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expectedItems uint64
		rate          float64
		wantM         uint64
		wantK         int
	}{
		{name: "zero items", expectedItems: 0, rate: 0.1, wantM: 1, wantK: 1},
		{name: "tiny m clamp", expectedItems: 1, rate: 0.9, wantM: 1, wantK: 1},
		{name: "probe clamp", expectedItems: 10, rate: 0.999, wantM: 1, wantK: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotM, gotK := Parameters(tc.expectedItems, tc.rate)
			if gotM != tc.wantM || gotK != tc.wantK {
				t.Fatalf("Parameters(%d, %v) = (%d, %d), want (%d, %d)", tc.expectedItems, tc.rate, gotM, gotK, tc.wantM, tc.wantK)
			}
		})
	}
}

type stubFilter struct {
	present   map[string]bool
	removed   []string
	contains  map[string]int
	addCalled int
}

func (f *stubFilter) Add(d cas.Digest) {
	f.addCalled++
	f.present[d.String()] = true
}

func (f *stubFilter) Contains(d cas.Digest) bool {
	if f.contains == nil {
		f.contains = map[string]int{}
	}
	f.contains[d.String()]++
	return f.present[d.String()]
}

func (f *stubFilter) Remove(d cas.Digest) {
	f.removed = append(f.removed, d.String())
	delete(f.present, d.String())
}

type stubBackend struct {
	putErr      error
	getErr      error
	existsErr   error
	deleteErr   error
	existsCalls int
	list        []cas.Digest
	stats       *cas.Stats
	stored      map[string][]byte
}

func (b *stubBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if b.putErr != nil {
		return b.putErr
	}
	if b.stored == nil {
		b.stored = map[string][]byte{}
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.stored[d.String()] = buf
	return nil
}

func (b *stubBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if b.getErr != nil {
		return nil, b.getErr
	}
	if buf, ok := b.stored[d.String()]; ok {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	return nil, cas.ErrNotFound
}

func (b *stubBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	b.existsCalls++
	if b.existsErr != nil {
		return false, b.existsErr
	}
	_, ok := b.stored[d.String()]
	return ok, nil
}

func (b *stubBackend) Delete(ctx context.Context, d cas.Digest) error {
	if b.deleteErr != nil {
		return b.deleteErr
	}
	delete(b.stored, d.String())
	return nil
}

func (b *stubBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.list, nil
}

func (b *stubBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.stats, nil
}

func TestNewGuardPanicsOnNilInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		be   cas.Backend
		f    guardFilter
	}{
		{name: "backend nil", be: nil, f: &stubFilter{present: map[string]bool{}}},
		{name: "filter nil", be: &stubBackend{stored: map[string][]byte{}}, f: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			_ = NewGuard(tc.be, tc.f)
		})
	}
}

func TestGuardPutAndFilterDelegation(t *testing.T) {
	ctx := context.Background()
	backend := &stubBackend{stored: map[string][]byte{}}
	filter := &stubFilter{present: map[string]bool{}}
	guard := NewGuard(backend, filter)
	d := cas.NewDigest([]byte("guard put"))

	if err := guard.Put(ctx, d, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if filter.addCalled != 1 {
		t.Fatalf("filter Add call count = %d, want 1", filter.addCalled)
	}
	if !filter.Contains(d) {
		t.Fatal("guard should add digest to filter")
	}
	if got, err := guard.Exists(ctx, d); err != nil || !got {
		t.Fatalf("guard.Exists = (%v, %v), want (true, nil)", got, err)
	}
}

func TestGuardExistsShortCircuitsOnNegativeBloomResult(t *testing.T) {
	ctx := context.Background()
	backend := &stubBackend{stored: map[string][]byte{}}
	filter := &stubFilter{present: map[string]bool{}}
	guard := NewGuard(backend, filter)
	d := cas.NewDigest([]byte("absent"))

	ok, err := guard.Exists(ctx, d)
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if ok {
		t.Fatal("Exists should reject a negative bloom result")
	}
	if backend.existsCalls != 0 {
		t.Fatalf("backend.Exists calls = %d, want 0 when bloom filter rejects item", backend.existsCalls)
	}
}

func TestGuardExistsUsesBackendAfterPositiveBloomResult(t *testing.T) {
	ctx := context.Background()
	backend := &stubBackend{stored: map[string][]byte{}}
	filter := &stubFilter{present: map[string]bool{}}
	guard := NewGuard(backend, filter)
	d := cas.NewDigest([]byte("present"))
	filter.present[d.String()] = true
	backend.stored[d.String()] = []byte("payload")

	ok, err := guard.Exists(ctx, d)
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !ok {
		t.Fatal("Exists should return true when backend confirms presence")
	}
	if backend.existsCalls != 1 {
		t.Fatalf("backend.Exists calls = %d, want 1", backend.existsCalls)
	}
}

func TestGuardZeroDigestSkipsBloomLookup(t *testing.T) {
	backend := &stubBackend{stored: map[string][]byte{}}
	filter := &stubFilter{present: map[string]bool{}}
	guard := NewGuard(backend, filter)

	ok, err := guard.Exists(context.Background(), cas.Digest{})
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if ok {
		t.Fatal("zero digest should never exist")
	}
	if backend.existsCalls != 0 {
		t.Fatalf("backend.Exists calls = %d, want 0 for zero digest", backend.existsCalls)
	}
}

func TestGuardDeleteRemovesWhenSupported(t *testing.T) {
	ctx := context.Background()
	d := cas.NewDigest([]byte("dig"))
	backend := &stubBackend{stored: map[string][]byte{d.String(): []byte("payload")}}
	filter := &stubFilter{present: map[string]bool{d.String(): true}}
	guard := NewGuard(backend, filter)

	if err := guard.Delete(ctx, d); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, exists := backend.stored[d.String()]; exists {
		t.Fatal("backend should delete digest")
	}
	if len(filter.removed) != 1 || filter.removed[0] != d.String() {
		t.Fatalf("filter.Remove calls = %v, want [%q]", filter.removed, d.String())
	}
}

func TestGuardDeletePropagatesBackendErrors(t *testing.T) {
	ctx := context.Background()
	d := cas.NewDigest([]byte("dig"))
	backend := &stubBackend{stored: map[string][]byte{d.String(): []byte("payload")}, deleteErr: errors.New("delete fail")}
	filter := &stubFilter{present: map[string]bool{d.String(): true}}
	guard := NewGuard(backend, filter)

	if err := guard.Delete(ctx, d); err == nil {
		t.Fatal("Delete() error = nil, want non-nil")
	}
	if _, exists := backend.stored[d.String()]; !exists {
		t.Fatal("backend delete should not mutate stored bytes when it reports an error")
	}
	if len(filter.removed) != 0 {
		t.Fatalf("filter.Remove calls = %v, want none on backend error", filter.removed)
	}
}

func TestGuardDelegatesGetListStatsAndFilter(t *testing.T) {
	ctx := context.Background()
	backend := &stubBackend{stored: map[string][]byte{}}
	filter := &stubFilter{present: map[string]bool{}}
	guard := NewGuard(backend, filter)

	d := cas.NewDigest([]byte("get me"))
	backend.stored[d.String()] = []byte("payload")
	backend.list = []cas.Digest{d}
	backend.stats = &cas.Stats{ObjectCount: 1, TotalSize: 7}

	reader, err := guard.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, err := io.ReadAll(reader); err != nil || !bytes.Equal(got, []byte("payload")) {
		t.Fatalf("Get() payload = %q, %v, want %q, nil", got, err, "payload")
	}
	if got, err := guard.List(ctx); err != nil || len(got) != 1 || got[0].String() != d.String() {
		t.Fatalf("List() = (%v, %v), want ([%v], nil)", got, err, d)
	}
	stats, err := guard.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats == nil || stats.ObjectCount != 1 || stats.TotalSize != 7 {
		t.Fatalf("Stats() = %#v, want ObjectCount=1 TotalSize=7", stats)
	}
	if guard.Filter() != filter {
		t.Fatal("Filter() returned wrong filter instance")
	}
}

func TestValidateFalsePositiveRate(t *testing.T) {
	for _, rate := range []float64{0, -0.1, 1, 2} {
		if err := ValidateFalsePositiveRate(rate, "test"); err == nil {
			t.Fatalf("ValidateFalsePositiveRate(%v) = nil, want non-nil", rate)
		}
	}
	if err := ValidateFalsePositiveRate(0.01, "test"); err != nil {
		t.Fatalf("ValidateFalsePositiveRate(0.01) = %v, want nil", err)
	}
}

func TestResolveIndexHash(t *testing.T) {
	defaultHash := ResolveIndexHash(nil)
	if defaultHash == nil {
		t.Fatal("ResolveIndexHash(nil) returned nil")
	}
	if got := defaultHash([]byte("abc"), 2); got != DefaultIndexHash([]byte("abc"), 2) {
		t.Fatal("ResolveIndexHash(nil) did not return the default hash")
	}

	custom := func(data []byte, i int) uint64 { return uint64(len(data) + i) }
	if got := ResolveIndexHash(custom)([]byte("abc"), 2); got != 5 {
		t.Fatalf("ResolveIndexHash(custom) = %d, want 5", got)
	}
}

func TestParametersAndIndices(t *testing.T) {
	m, k := Parameters(100, 0.01)
	if m == 0 || k == 0 {
		t.Fatalf("Parameters(100, 0.01) = (%d, %d), want positive values", m, k)
	}

	custom := func(data []byte, i int) uint64 {
		return uint64(len(data) + i)
	}
	got := Indices(custom, []byte("abc"), 3, 10)
	want := []uint64{3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("Indices() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Indices()[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
