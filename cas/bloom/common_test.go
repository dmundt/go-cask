package bloom

import "testing"

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
			gotM, gotK := Parameters(tc.expectedItems, tc.rate)
			if gotM == 0 || gotK <= 0 {
				t.Fatalf("Parameters returned invalid dimensions: m=%d k=%d", gotM, gotK)
			}
			if tc.expectedItems == 0 && (gotM != 1 || gotK != 1) {
				t.Fatalf("Parameters(0, %v) = (%d, %d), want (1, 1)", tc.rate, gotM, gotK)
			}
		})
	}
}

func TestIndicesMatchesExpectedProbeSequence(t *testing.T) {
	custom := func(data []byte, i int) uint64 {
		if len(data) == 0 {
			return uint64(i)
		}
		return uint64(data[0]) + uint64(i)
	}
	got := Indices(custom, []byte{9}, 4, 10)
	if len(got) != 4 {
		t.Fatalf("Indices length = %d, want 4", len(got))
	}
	want := []uint64{9, 10, 11, 12}
	for i := range got {
		if got[i] != want[i]%10 {
			t.Fatalf("Indices[%d] = %d, want %d", i, got[i], want[i]%10)
		}
	}
}
