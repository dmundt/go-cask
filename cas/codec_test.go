package cas

import (
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

func TestJSONCodecDecodeError(t *testing.T) {
	c := JSONCodec[testNote]{}
	if _, err := c.Decode([]byte("not json")); err == nil {
		t.Fatal("Decode of garbage must fail")
	}
	if _, err := c.Decode(nil); err == nil {
		t.Fatal("Decode of empty must fail")
	}
}

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
