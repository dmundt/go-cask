package main

import "testing"

func FuzzMustStringAndBool(f *testing.F) {
	f.Add("hash", "deadbeef", true)
	f.Add("name", "demo", false)
	f.Add("token", "abc", true)
	f.Fuzz(func(t *testing.T, key, value string, ok bool) {
		m := map[string]any{key: value, "flag": ok}
		if got, err := mustString(m, key); err != nil || got != value {
			if err == nil {
				t.Fatalf("mustString(%q) = %q, want %q", key, got, value)
			}
			return
		}
		if got, err := mustBool(m, "flag"); err != nil || got != ok {
			if err == nil {
				t.Fatalf("mustBool(flag) = %v, want %v", got, ok)
			}
			return
		}
	})
}
