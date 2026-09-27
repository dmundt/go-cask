package cbor

import (
	"errors"
	"testing"
)

// nestedArrayChain builds a CBOR value nested depth levels deep: one array(1)
// head per level and an unsigned 0 innermost. It is the shape a crafted payload
// uses to drive the decoder's recursion — the reproduction in go-cask#356, where
// a chain of about two million levels exhausted the goroutine stack and aborted
// the process with a fatal error a caller's recover cannot catch.
func nestedArrayChain(depth int) []byte {
	data := make([]byte, 0, depth+1)
	for range depth {
		data = append(data, 0x81) // array(1)
	}
	return append(data, 0x00) // unsigned 0
}

// nestedMapChain builds depth levels of single-entry maps — 0xa1 0x61 'k' per
// level, then an unsigned 0 — so the bound is pinned for the map arm as well.
func nestedMapChain(depth int) []byte {
	data := make([]byte, 0, depth*3+1)
	for range depth {
		data = append(data, 0xa1, 0x61, 'k') // map(1), text(1) "k"
	}
	return append(data, 0x00)
}

// TestDecodeRejectsNestingPastTheLimit is the go-cask#356 regression: a payload
// nested one level past MaxDepth must return ErrTooDeep, never recurse until the
// stack is exhausted. The two-million-level case is the payload measured on this
// tree; before the bound existed it killed the test binary with
// "fatal error: stack overflow", so this test passing at all is part of the
// assertion.
func TestDecodeRejectsNestingPastTheLimit(t *testing.T) {
	decoders := []struct {
		name   string
		decode func([]byte) error
	}{
		{"NewValue", func(data []byte) error { _, err := NewValue().Decode(data); return err }},
		{"NewMap", func(data []byte) error { _, err := NewMap().Decode(data); return err }},
	}

	payloads := []struct {
		name string
		data []byte
	}{
		{"one level past the limit", nestedArrayChain(MaxDepth + 1)},
		{"two levels past the limit", nestedArrayChain(MaxDepth + 2)},
		{"a thousand levels past the limit", nestedArrayChain(1000)},
		{"the two-million-level reproduction", nestedArrayChain(2_000_000)},
		{"maps one level past the limit", nestedMapChain(MaxDepth + 1)},
		{"maps a thousand levels past the limit", nestedMapChain(1000)},
	}

	for _, decoder := range decoders {
		for _, payload := range payloads {
			t.Run(decoder.name+" "+payload.name, func(t *testing.T) {
				err := decoder.decode(payload.data)
				if !errors.Is(err, ErrTooDeep) {
					t.Fatalf("Decode(%s) = %v, want ErrTooDeep", payload.name, err)
				}
			})
		}
	}
}

// TestDecodeAcceptsNestingAtTheLimit pins the accepting side: a payload exactly
// MaxDepth levels deep still decodes to the equivalent value, so the bound
// rejects only nesting no real metadata or manifest payload carries.
func TestDecodeAcceptsNestingAtTheLimit(t *testing.T) {
	got, err := NewValue().Decode(nestedArrayChain(MaxDepth))
	if err != nil {
		t.Fatalf("Decode(%d nested arrays) = %v, want success", MaxDepth, err)
	}
	current := got
	for level := range MaxDepth {
		items, ok := current.([]any)
		if !ok {
			t.Fatalf("level %d decoded to %T, want []any", level, current)
		}
		if len(items) != 1 {
			t.Fatalf("level %d holds %d items, want 1", level, len(items))
		}
		current = items[0]
	}
	if current != int64(0) {
		t.Fatalf("innermost value = %#v, want int64(0)", current)
	}

	m, err := NewMap().Decode(nestedMapChain(MaxDepth))
	if err != nil {
		t.Fatalf("Decode(%d nested maps) = %v, want success", MaxDepth, err)
	}
	current = m
	for level := range MaxDepth {
		entries, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("level %d decoded to %T, want map[string]any", level, current)
		}
		if len(entries) != 1 {
			t.Fatalf("level %d holds %d entries, want 1", level, len(entries))
		}
		current = entries["k"]
	}
	if current != int64(0) {
		t.Fatalf("innermost map value = %#v, want int64(0)", current)
	}
}

// TestDecodeDepthBoundIsCheckedBeforeThePayload pins the entry check itself:
// decodeOne rejects a depth already past MaxDepth whatever the value at that
// depth is, so no input can reach the decoder below the bound.
func TestDecodeDepthBoundIsCheckedBeforeThePayload(t *testing.T) {
	if _, _, err := decodeOne(nestedArrayChain(MaxDepth), 0); err != nil {
		t.Fatalf("decodeOne at the limit = %v, want success", err)
	}
	if _, _, err := decodeOne(nestedArrayChain(MaxDepth+1), 0); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("decodeOne past the limit = %v, want ErrTooDeep", err)
	}
	// A scalar carries no nesting of its own, so only the depth argument can
	// reject it — which is what makes the check a bound on recursion rather
	// than on the value shape.
	if _, _, err := decodeOne([]byte{0x00}, MaxDepth+1); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("decodeOne(0x00, MaxDepth+1) = %v, want ErrTooDeep", err)
	}
	// Truncation keeps its own error, so the two are distinguishable.
	if _, _, err := decodeOne([]byte{0x43, 0x01, 0x02}, 0); err == nil || errors.Is(err, ErrTooDeep) {
		t.Fatalf("decodeOne(truncated) = %v, want the truncation error, not ErrTooDeep", err)
	}
}

// FuzzDecodeValue drives the decoder with arbitrary bytes rather than with this
// package's own encoder output, which is what leaves the round-trip target alone
// unable to reach a hostile nesting chain (go-cask#356). Its seeds are the
// deep-but-bounded chain and the chain one level past the bound; a panic or a
// stack overflow fails the target, and the seeds run with the ordinary suite.
func FuzzDecodeValue(f *testing.F) {
	f.Add(nestedArrayChain(1))
	f.Add(nestedMapChain(1))
	f.Add(nestedArrayChain(MaxDepth))
	f.Add(nestedArrayChain(MaxDepth + 1))
	f.Add(nestedMapChain(MaxDepth + 1))
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NewValue().Decode(data)
	})
}
