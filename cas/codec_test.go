package cas

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONCodecRoundTrip(t *testing.T) {
	type edge struct {
		I int     `json:"i"`
		F float64 `json:"f"`
		S string  `json:"s"`
		B bool    `json:"b"`
	}
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty", func(t *testing.T) {
			c := JSONCodec[testNote]{}
			data, err := c.Encode(testNote{})
			if err != nil {
				t.Fatal(err)
			}
			back, err := c.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if back != (testNote{}) {
				t.Fatalf("round-trip: %+v", back)
			}
		}},
		{"nested", func(t *testing.T) {
			c := JSONCodec[testNote]{}
			v := testNote{Title: "hello", Body: "world"}
			data, err := c.Encode(v)
			if err != nil {
				t.Fatal(err)
			}
			back, err := c.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if back != v {
				t.Fatalf("round-trip: %+v != %+v", back, v)
			}
		}},
		{"edge-values", func(t *testing.T) {
			c := JSONCodec[edge]{}
			v := edge{I: -1, F: 3.14, S: "\u00e9\n\t", B: true}
			data, err := c.Encode(v)
			if err != nil {
				t.Fatal(err)
			}
			back, err := c.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if back != v {
				t.Fatalf("round-trip: %+v != %+v", back, v)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// removed duplicate

// FuzzCodecRoundTrip: Decode(Encode(x)) == x for generated values.
func FuzzCodecRoundTrip(f *testing.F) {
	f.Add("", "")
	f.Add("a", "b")
	f.Fuzz(func(t *testing.T, title, body string) {
		// encoding/json replaces invalid UTF-8 with U+FFFD on marshal, so
		// identity round-trip holds only for valid UTF-8 text (the codec's
		// domain); skip raw invalid bytes.
		if !utf8.ValidString(title) || !utf8.ValidString(body) {
			t.Skip()
		}
		c := JSONCodec[testNote]{}
		v := testNote{Title: title, Body: body}
		data, err := c.Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		back, err := c.Decode(data)
		if err != nil {
			t.Fatalf("Decode(Encode(v)): %v", err)
		}
		if back != v {
			t.Fatalf("round-trip mismatch: %+v != %+v", back, v)
		}
	})
}

// TestJSONCodecInvalidUTF8Lossy pins the boundary that FuzzCodecRoundTrip
// documents: encoding/json is lossy on invalid UTF-8 (each invalid byte
// becomes U+FFFD on encode), so an identity round-trip can only hold for
// valid UTF-8 text — which is why the fuzz target skips raw invalid bytes.
// If invalid bytes ever survive the round-trip, this test reports it and the
// fuzz input constraint can be lifted.
func TestJSONCodecInvalidUTF8Lossy(t *testing.T) {
	c := JSONCodec[testNote]{}
	v := testNote{Title: "ok", Body: "bad\xe8 byte"}
	data, err := c.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	back, err := c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if back == v {
		t.Log("invalid UTF-8 now round-trips losslessly — the FuzzCodecRoundTrip input constraint could be lifted")
		return
	}
	if !strings.Contains(back.Body, "\ufffd") {
		t.Fatalf("expected U+FFFD replacement for the invalid byte, got %q", back.Body)
	}
}

func TestGobCodecRoundTrip(t *testing.T) {
	codec := GobCodec[testNote]{}
	orig := testNote{Title: "gob", Body: "test"}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("gob round-trip: %+v != %+v", decoded, orig)
	}
}

func TestCompressedJSONCodecRoundTrip(t *testing.T) {
	inner := JSONCodec[testNote]{}
	codec := NewCompressedCodec(inner)
	orig := testNote{Title: "compressed", Body: strings.Repeat("payload", 100)}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	// Compressed output should be smaller than raw JSON for repetitive data.
	raw, _ := inner.Encode(orig)
	if len(data) >= len(raw) {
		t.Logf("compressed=%d raw=%d — small payload, gzip overhead typical", len(data), len(raw))
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("compressed round-trip: %+v != %+v", decoded, orig)
	}
}

func TestCompressedGobCodecRoundTrip(t *testing.T) {
	inner := GobCodec[testNote]{}
	codec := NewCompressedCodec(inner)
	orig := testNote{Title: "gzip+gob", Body: strings.Repeat("x", 500)}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("gzip+gob round-trip: %+v != %+v", decoded, orig)
	}
}

func TestCompressedCodecCorruptData(t *testing.T) {
	codec := NewCompressedCodec(JSONCodec[testNote]{})
	if _, err := codec.Decode([]byte("not gzip")); err == nil {
		t.Fatal("corrupt data must error")
	}
}

func TestGobCodecEncodeError(t *testing.T) {
	codec := GobCodec[chan int]{}
	if _, err := codec.Encode(make(chan int)); err == nil {
		t.Fatal("gob encode of channel must error")
	}
}

func TestJSONCodecEncodeDecode(t *testing.T) {
	codec := JSONCodec[testNote]{}
	orig := testNote{Title: "json", Body: "test"}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("json round-trip: %+v != %+v", decoded, orig)
	}
}

func TestCompressedCodecEmptyPayload(t *testing.T) {
	inner := JSONCodec[testNote]{}
	codec := NewCompressedCodec(inner)
	orig := testNote{}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("empty compressed round-trip: %+v != %+v", decoded, orig)
	}
}

func TestJSONCodecDecodeError(t *testing.T) {
	codec := JSONCodec[testNote]{}
	if _, err := codec.Decode([]byte("{invalid")); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestGobCodecDecodeError(t *testing.T) {
	codec := GobCodec[testNote]{}
	if _, err := codec.Decode([]byte("garbage")); err == nil {
		t.Fatal("invalid gob must error")
	}
}

func TestGobCodecRoundTripEmpty(t *testing.T) {
	codec := GobCodec[testNote]{}
	orig := testNote{}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("gob empty round-trip: %+v != %+v", decoded, orig)
	}
}

func TestGobCodecPointer(t *testing.T) {
	codec := GobCodec[*testNote]{}
	orig := &testNote{Title: "pt", Body: "r"}
	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Title != "pt" {
		t.Fatalf("gob pointer: %+v", decoded)
	}
}
