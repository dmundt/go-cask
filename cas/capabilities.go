package cas

import (
	"context"
	"time"
)

// Capabilities reports which optional maintenance operations a Backend
// supports beyond the required Backend contract (Put/Get/Exists/Delete/List/
// Stats).
//
// Verify and Sweep are always true: VerifyAll and Sweep are generic
// implementations that need nothing beyond the minimal Backend interface
// (Get/List, and List/Delete respectively), so every backend supports them.
// Clean and Stat report whether the backend opts in to the corresponding
// optional interface (Cleaner, Statter); a caller that needs age-based
// retention (Sweep with SweepOptions.MinAge > 0) requires Stat.
//
// A concrete backend such as fs.Backend may additionally expose its own
// Verify/GC/Prune/Clean/Size/ModTime methods as a faster, backend-native path;
// CapabilitiesOf and the generic functions in this package describe the
// portable baseline every backend guarantees, not the fastest path a specific
// backend can take.
type Capabilities struct {
	// Verify reports whether VerifyAll (and Verify) can run against the
	// backend. Always true.
	Verify bool
	// Sweep reports whether Sweep can run against the backend. Always true.
	Sweep bool
	// Clean reports whether the backend implements Cleaner.
	Clean bool
	// Stat reports whether the backend implements Statter, which is
	// required for age-based Sweep (SweepOptions.MinAge > 0).
	Stat bool
}

// CapabilitiesOf reports which optional maintenance operations raw supports.
func CapabilitiesOf(backend Backend) Capabilities {
	_, clean := backend.(Cleaner)
	_, stat := backend.(Statter)
	return Capabilities{
		Verify: true,
		Sweep:  true,
		Clean:  clean,
		Stat:   stat,
	}
}

// Cleaner is implemented by backends that can remove their own
// implementation-specific orphaned scratch state — crash-leftover temp files,
// for example. Clean removes items older than olderThan (olderThan <= 0
// removes them all) and returns the count removed.
//
// Clean is backend-specific because "orphaned scratch state" is not a concept
// the minimal Backend interface exposes; a backend that never leaves any
// (e.g. mem.Backend) simply does not implement Cleaner.
type Cleaner interface {
	Clean(ctx context.Context, olderThan time.Duration) (int, error)
}

// Statter is implemented by backends that can report physical per-object
// metadata beyond the content-addressed bytes: size and modification time.
// This is physical backend metadata, not a content-addressed object field —
// two backends storing the same digest need not agree on it.
//
// Sweep uses ModTime for age-based retention (SweepOptions.MinAge); a backend
// without Statter supports only unconditional Sweep (MinAge <= 0).
type Statter interface {
	Size(ctx context.Context, d Digest) (int64, error)
	ModTime(ctx context.Context, d Digest) (time.Time, error)
}
