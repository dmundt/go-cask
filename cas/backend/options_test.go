package backend

import "testing"

func TestOptionMutatesConfig(t *testing.T) {
	type cfg struct{ value int }
	c := &cfg{}
	opt := func(v any) {
		if cc, ok := v.(*cfg); ok {
			cc.value = 7
		}
	}
	opt(c)
	if c.value != 7 {
		t.Fatalf("Option mutated cfg.value = %d, want 7", c.value)
	}
}
