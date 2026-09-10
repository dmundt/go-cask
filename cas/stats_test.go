package cas

import "testing"

// TestStatsString covers Stats.String() for the counts the core can report:
// there is no per-algorithm breakdown, because the core does not know which
// algorithm produced a digest (cas-core §4.2).
func TestStatsString(t *testing.T) {
	tests := []struct {
		name string
		st   Stats
		want string
	}{
		{
			name: "zero value",
			st:   Stats{},
			want: "0 objects, 0 bytes",
		},
		{
			name: "counts and size",
			st:   Stats{ObjectCount: 5, TotalSize: 1024},
			want: "5 objects, 1024 bytes",
		},
		{
			name: "size without objects",
			st:   Stats{TotalSize: 7},
			want: "0 objects, 7 bytes",
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
