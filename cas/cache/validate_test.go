package cache

import "testing"

func TestValidateMaxSize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		maxSize int
		wantErr bool
	}{
		{name: "positive", maxSize: 10, wantErr: false},
		{name: "zero", maxSize: 0, wantErr: true},
		{name: "negative", maxSize: -1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMaxSize(tc.maxSize, "cache/test")
			if tc.wantErr && err == nil {
				t.Fatal("ValidateMaxSize returned nil error, want non-nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateMaxSize returned unexpected error: %v", err)
			}
		})
	}
}
