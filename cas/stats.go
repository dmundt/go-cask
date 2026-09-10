package cas

import "fmt"

// Stats summarizes the store contents: total size in bytes and total object
// count. Both built-in backends (fs, mem) return it from their Stats method so
// callers can treat them interchangeably.
//
// There is no per-algorithm breakdown: the core does not know which algorithm
// produced a digest (cas-core §4.2), so it cannot group objects by one. A client
// that needs that groups its own digests.
type Stats struct {
	TotalSize   int64
	ObjectCount int64
}

// String renders a one-line human summary of the stats.
func (st Stats) String() string {
	return fmt.Sprintf("%d objects, %d bytes", st.ObjectCount, st.TotalSize)
}
