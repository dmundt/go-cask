package cas

import "testing"

// TestStatsString covers Stats.String() across empty, single-algorithm, and
// multi-algorithm cases, including its deterministic (alphabetical) ordering
// of the per-algorithm counts.
func TestStatsString(t *testing.T) {
	tests := []struct {
		name string
		st   Stats
		want string
	}{
		{
			name: "zero value",
			st:   Stats{},
			want: "0 objects, 0 bytes []",
		},
		{
			name: "nil algorithm counts",
			st:   Stats{ObjectCount: 3, TotalSize: 150, AlgorithmCounts: nil},
			want: "3 objects, 150 bytes []",
		},
		{
			name: "empty non-nil algorithm counts",
			st:   Stats{ObjectCount: 1, TotalSize: 8, AlgorithmCounts: map[string]int{}},
			want: "1 objects, 8 bytes []",
		},
		{
			name: "single algorithm",
			st: Stats{
				ObjectCount:     5,
				TotalSize:       1024,
				AlgorithmCounts: map[string]int{"sha256": 5},
			},
			want: "5 objects, 1024 bytes [sha256=5]",
		},
		{
			name: "multiple algorithms sorted",
			st: Stats{
				ObjectCount:     4,
				TotalSize:       40,
				AlgorithmCounts: map[string]int{"sha256": 2, "sha1": 1, "blake3": 1},
			},
			// Algorithm names render in alphabetical order, not map order.
			want: "4 objects, 40 bytes [blake3=1, sha1=1, sha256=2]",
		},
		{
			name: "zero count algorithm still listed",
			st: Stats{
				ObjectCount:     2,
				TotalSize:       20,
				AlgorithmCounts: map[string]int{"sha256": 2, "sha1": 0},
			},
			want: "2 objects, 20 bytes [sha1=0, sha256=2]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStatsStringDeterministic pins String() to be stable across calls for the
// same Stats value (no map-iteration-order dependence).
func TestStatsStringDeterministic(t *testing.T) {
	st := Stats{
		ObjectCount: 7,
		TotalSize:   99,
		AlgorithmCounts: map[string]int{
			"sha512": 1, "sha256": 4, "md5": 2,
		},
	}
	first := st.String()
	for i := 0; i < 50; i++ {
		if got := st.String(); got != first {
			t.Fatalf("String() changed between calls: %q vs %q", first, got)
		}
	}
}
