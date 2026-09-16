package backend

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

type mockBackend struct {
	items map[string][]byte
}

func (m *mockBackend) Put(_ context.Context, d cas.Digest, r io.Reader) error {
	if m.items == nil {
		m.items = map[string][]byte{}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.items[d.String()] = append([]byte(nil), b...)
	return nil
}

func (m *mockBackend) Get(_ context.Context, d cas.Digest) (io.ReadCloser, error) {
	if b, ok := m.items[d.String()]; ok {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	return nil, cas.ErrNotFound
}

func (m *mockBackend) Exists(_ context.Context, d cas.Digest) (bool, error) {
	_, ok := m.items[d.String()]
	return ok, nil
}

func (m *mockBackend) Delete(_ context.Context, d cas.Digest) error {
	delete(m.items, d.String())
	return nil
}

func (m *mockBackend) List(_ context.Context) ([]cas.Digest, error) {
	out := make([]cas.Digest, 0, len(m.items))
	for key := range m.items {
		d, err := cas.ParseDigest(key)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *mockBackend) Stats(_ context.Context) (*cas.Stats, error) {
	var total int64
	for _, v := range m.items {
		total += int64(len(v))
	}
	return &cas.Stats{ObjectCount: int64(len(m.items)), TotalSize: total}, nil
}

func TestMockBackendContract(t *testing.T) {
	ctx := context.Background()
	raw := &mockBackend{items: map[string][]byte{}}
	d := cas.NewDigest([]byte("mock backend"))
	payload := []byte("mock backend payload")

	if err := raw.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	ok, err := raw.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	got, err := raw.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer got.Close()
	buf, err := io.ReadAll(got)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("Get() = %q, want %q", buf, payload)
	}

	if err := raw.Delete(ctx, d); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	ok, err = raw.Exists(ctx, d)
	if err != nil || ok {
		t.Fatalf("Exists() after Delete = (%v, %v), want (false, nil)", ok, err)
	}

	stats, err := raw.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.ObjectCount != 0 || stats.TotalSize != 0 {
		t.Fatalf("Stats() = %#v, want empty summary", stats)
	}
}
