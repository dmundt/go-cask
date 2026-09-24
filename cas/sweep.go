package cas

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SweepOptions configures Sweep.
type SweepOptions struct {
	// MinAge, when > 0, keeps an unreachable object if it is younger than
	// MinAge — a grace period for an object a concurrent writer just
	// created but has not yet referenced. MinAge > 0 requires the backend
	// to implement Statter (ErrUnsupported otherwise).
	MinAge time.Duration
	// DryRun, when true, reports which digests would be deleted without
	// deleting them.
	DryRun bool
}

// Sweep performs mark-and-sweep reclamation over any Backend, using only the
// minimal Backend interface (List, Exists, Delete) plus, when
// SweepOptions.MinAge > 0, the optional Statter interface for age-based
// retention. reachable MUST already be the complete, transitively-closed set of
// live digests — Sweep never follows references itself; use Reachable (cas
// package, single type) or cas/repo.Reachable (cross-type) to build it from a
// set of roots first.
//
// Sweep is the portable fallback every backend supports (Capabilities.Sweep is
// always true). A concrete backend may still expose faster backend-native
// GC/Prune methods; Sweep is the documented way to reclaim space against a
// backend, such as packfs, that does not.
//
// A digest-named entry the backend cannot address is not an object and no sweep
// may touch it (cas-core §4.4): an fs store with a wider fan-out lists any
// lowercase-hex file name, including one too short for its layout. Sweep asks
// the backend before it deletes anything — Exists applies the backend's own key
// validation without mutating — so the portable path skips exactly what the
// fs-native GC/Prune skip, a stray name cannot abort the sweep halfway, and a
// dry run reports the same set a real run would reclaim.
func Sweep(ctx context.Context, backend Backend, reachable map[string]bool, opts SweepOptions) ([]Digest, error) {
	if backend == nil {
		return nil, fmt.Errorf("cas: sweep: nil backend")
	}
	var statter Statter
	if opts.MinAge > 0 {
		s, ok := backend.(Statter)
		if !ok {
			return nil, fmt.Errorf("%w: age-based sweep requires Statter", ErrUnsupported)
		}
		statter = s
	}
	digests, err := backend.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var doomed []Digest
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if reachable[d.String()] {
			continue
		}
		// List reports any digest-named entry; only the backend knows which of
		// them its own layout can address. Ask before anything is deleted, so a
		// key it rejects — or one a concurrent sweep already removed — is never
		// a candidate in either the reporting or the deleting pass.
		exists, err := backend.Exists(ctx, d)
		if err != nil {
			if errors.Is(err, ErrInvalidDigest) {
				continue // not addressable by this backend: not an object
			}
			return nil, err
		}
		if !exists {
			continue // deleted by a concurrent sweep
		}
		if statter != nil {
			mt, err := statter.ModTime(ctx, d)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					continue // deleted by a concurrent sweep
				}
				if errors.Is(err, ErrInvalidDigest) {
					continue // not addressable by this backend: not an object
				}
				return nil, err
			}
			if now.Sub(mt) < opts.MinAge {
				continue
			}
		}
		doomed = append(doomed, d)
	}
	if !opts.DryRun {
		for _, d := range doomed {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := backend.Delete(ctx, d); err != nil {
				return nil, err
			}
		}
	}
	return doomed, nil
}
