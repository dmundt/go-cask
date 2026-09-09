package cas

import (
	"fmt"
	"sort"
	"strings"
)

// StoreStats summarizes the store contents: per-algorithm object counts,
// total size in bytes, and total object count. Both built-in backends (fs,
// mem) return it from Stats so callers can treat them interchangeably.
type StoreStats struct {
	AlgorithmCounts map[string]int
	TotalSize       int64
	ObjectCount     int64
}

// String renders a one-line human summary of the stats.
func (st StoreStats) String() string {
	algos := make([]string, 0, len(st.AlgorithmCounts))
	for a := range st.AlgorithmCounts {
		algos = append(algos, a)
	}
	sort.Strings(algos)
	parts := make([]string, 0, len(algos))
	for _, a := range algos {
		parts = append(parts, fmt.Sprintf("%s=%d", a, st.AlgorithmCounts[a]))
	}
	return fmt.Sprintf("%d objects, %d bytes [%s]", st.ObjectCount, st.TotalSize, strings.Join(parts, ", "))
}
