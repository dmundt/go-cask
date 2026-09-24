package bloom

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
)

type fuzzFilter struct {
	items map[string]bool
}

func (f *fuzzFilter) Add(d cas.Digest) {
	if f.items == nil {
		f.items = map[string]bool{}
	}
	f.items[d.String()] = true
}

func (f *fuzzFilter) Contains(d cas.Digest) bool {
	if f.items == nil {
		return false
	}
	return f.items[d.String()]
}

func (f *fuzzFilter) Remove(d cas.Digest) {
	if f.items != nil {
		delete(f.items, d.String())
	}
}

func FuzzGuardRoundTrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte("bloom"))
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})

	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) == 0 {
			in = []byte{0x42}
		}
		ctx := context.Background()
		backend := backmem.New()
		filter := &fuzzFilter{items: map[string]bool{}}
		guard, err := NewGuard(backend, filter)
		if err != nil {
			t.Fatalf("NewGuard() error = %v", err)
		}
		d := cas.NewDigest(in)
		payload := slices.Clone(in)

		if err := guard.Put(ctx, d, io.NopCloser(bytes.NewReader(payload))); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if !filter.Contains(d) {
			t.Fatal("filter should contain digest after Put")
		}
		ok, err := guard.Exists(ctx, d)
		if err != nil || !ok {
			t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
		}
		if err := guard.Delete(ctx, d); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if filter.Contains(d) {
			t.Fatal("filter should drop digest after Delete")
		}
		ok, err = guard.Exists(ctx, d)
		if err != nil || ok {
			t.Fatalf("Exists() after Delete = (%v, %v), want (false, nil)", ok, err)
		}
	})
}
