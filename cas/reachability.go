package cas

import (
	"context"
	"fmt"
)

// ReferenceLister reports the direct references of the object stored at d,
// letting a reachability walk run over an otherwise opaque byte store without
// the core needing to know a concrete object model. A typed layer that can
// list an object's References() (Object[T], a per-type Store[T], or a
// multi-type registry/resolver) satisfies this trivially; Reachable stays
// entirely at the Digest level so it has no dependency on any single type.
type ReferenceLister interface {
	References(ctx context.Context, d Digest) ([]Digest, error)
}

// ReferenceListerFunc adapts a plain function to a ReferenceLister.
type ReferenceListerFunc func(ctx context.Context, d Digest) ([]Digest, error)

// References calls f.
func (f ReferenceListerFunc) References(ctx context.Context, d Digest) ([]Digest, error) {
	return f(ctx, d)
}

// Reachable computes the complete, transitively-closed set of digests
// reachable from roots by repeatedly calling refs.References until no new
// digest is discovered. It visits each digest at most once and is safe for
// any acyclic or cyclic graph (a cycle only stops expansion early; content
// addressing makes a genuine self-reference impossible, but a store written
// by another tool is not re-verified before this walk runs).
//
// This is the documented, correct way to build the reachable set that
// Backend.GC and Backend.Prune require: both take an already-expanded
// reachable set and never follow references themselves. Passing only
// entry-point roots to GC/Prune without first calling Reachable (or an
// equivalent typed walk) silently deletes anything those roots reference.
func Reachable(ctx context.Context, refs ReferenceLister, roots []Digest) (map[string]bool, error) {
	reachable := make(map[string]bool, len(roots))
	queue := make([]Digest, 0, len(roots))
	for _, r := range roots {
		if r.IsZero() {
			continue // an absent reference is not a root
		}
		key := r.String()
		if reachable[key] {
			continue
		}
		reachable[key] = true
		queue = append(queue, r)
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := queue[0]
		queue = queue[1:]
		direct, err := refs.References(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("cas: reachable: expand %s: %w", d, err)
		}
		for _, next := range direct {
			if next.IsZero() {
				continue
			}
			key := next.String()
			if reachable[key] {
				continue
			}
			reachable[key] = true
			queue = append(queue, next)
		}
	}
	return reachable, nil
}
