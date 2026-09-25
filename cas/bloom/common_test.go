package bloom

import (
	"math"
	"testing"
)

func TestValidateFalsePositiveRateEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rate    float64
		wantErr bool
	}{
		{name: "valid-small", rate: 0.01},
		{name: "valid-medium", rate: 0.5},
		{name: "zero", rate: 0, wantErr: true},
		{name: "one", rate: 1, wantErr: true},
		{name: "negative", rate: -0.1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFalsePositiveRate(tc.rate, "test")
			if tc.wantErr && err == nil {
				t.Fatal("ValidateFalsePositiveRate returned nil error, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateFalsePositiveRate returned unexpected error: %v", err)
			}
		})
	}
}

func TestResolveIndexHashSelection(t *testing.T) {
	custom := func(data []byte, i int) uint64 { return uint64(len(data) + i) }
	if got := ResolveIndexHash(nil); got == nil {
		t.Fatal("ResolveIndexHash(nil) returned nil, want default index hash")
	}
	if got := ResolveIndexHash(custom); got == nil {
		t.Fatal("ResolveIndexHash(custom) returned nil, want custom hash")
	}
	if got := ResolveIndexHash(custom)([]byte("abc"), 2); got != 5 {
		t.Fatalf("ResolveIndexHash(custom)(...) = %d, want 5", got)
	}
}

func TestParametersEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expectedItems uint64
		rate          float64
		wantM         uint64
		wantK         int
	}{
		{name: "zero items", expectedItems: 0, rate: 0.1, wantM: 1, wantK: 1},
		{name: "normal config", expectedItems: 1000, rate: 0.01},
		{name: "tiny rate", expectedItems: 100, rate: 0.0001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotM, gotK, err := Parameters(tc.expectedItems, tc.rate)
			if err != nil {
				t.Fatalf("Parameters(%d, %v) returned unexpected error: %v", tc.expectedItems, tc.rate, err)
			}
			if gotM == 0 || gotK <= 0 {
				t.Fatalf("Parameters returned invalid dimensions: m=%d k=%d", gotM, gotK)
			}
			if tc.expectedItems == 0 && (gotM != 1 || gotK != 1) {
				t.Fatalf("Parameters(0, %v) = (%d, %d), want (1, 1)", tc.rate, gotM, gotK)
			}
		})
	}
}

func TestParametersRejectsUnrepresentableRequests(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expectedItems uint64
		rate          float64
	}{
		{name: "unbounded items", expectedItems: 4e15, rate: 0.01},
		{name: "just above ceiling", expectedItems: MaxBits / 2, rate: 0.01},
		{name: "nan rate", expectedItems: 100, rate: math.NaN()},
		{name: "zero rate", expectedItems: 100, rate: 0},
		{name: "negative rate", expectedItems: 100, rate: -0.5},
		{name: "rate above one", expectedItems: 100, rate: 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, k, err := Parameters(tc.expectedItems, tc.rate)
			if err == nil {
				t.Fatalf("Parameters(%d, %v) = (%d, %d), want error", tc.expectedItems, tc.rate, m, k)
			}
			if m != 0 || k != 0 {
				t.Fatalf("Parameters(%d, %v) returned m=%d k=%d with an error, want zeros", tc.expectedItems, tc.rate, m, k)
			}
		})
	}
}

func TestParametersStaysWithinCeiling(t *testing.T) {
	m, k, err := Parameters(MaxBits/10, 0.01)
	if err != nil {
		t.Fatalf("Parameters at the ceiling returned unexpected error: %v", err)
	}
	if m > MaxBits {
		t.Fatalf("Parameters returned m=%d, above MaxBits=%d", m, MaxBits)
	}
	if k <= 0 {
		t.Fatalf("Parameters returned k=%d, want a positive probe count", k)
	}
}
