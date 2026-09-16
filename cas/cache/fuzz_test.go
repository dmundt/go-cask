package cache

import "testing"

func FuzzValidateMaxSize(f *testing.F) {
	f.Add(1)
	f.Add(0)
	f.Add(-1)
	f.Add(1024)
	f.Add(-128)

	f.Fuzz(func(t *testing.T, maxSize int) {
		err := ValidateMaxSize(maxSize, "fuzz")
		if maxSize > 0 && err != nil {
			t.Fatalf("ValidateMaxSize(%d) returned %v, want nil", maxSize, err)
		}
		if maxSize <= 0 && err == nil {
			t.Fatalf("ValidateMaxSize(%d) returned nil, want error", maxSize)
		}
	})
}
