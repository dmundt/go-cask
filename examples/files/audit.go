// Audit command for the files example: reports every stored object's
// derived state — verified / orphaned / corrupt / unverified — computed
// from the existing maintenance operations, never persisted.
//
// Reachability is marked from HEAD (the store's only root): objects the
// commit graph cannot reach are orphaned (GC candidates). Integrity is
// checked per object with the explicit cas.Verifier layer — a recompute from
// the object's own stored bytes, with nothing persisted beside it — unless
// -no-verify is given, in which case reachable objects are simply
// "unverified".
package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/gitlike"
)

// auditState is one of the four derived states an audit run assigns.
type auditState string

const (
	stateVerified   auditState = "verified"   // intact and reachable from HEAD
	stateOrphaned   auditState = "orphaned"   // intact but unreachable (GC candidate)
	stateCorrupt    auditState = "corrupt"    // Verify failed (bit rot / tampering)
	stateUnverified auditState = "unverified" // reachable; integrity not checked (-no-verify)
)

// auditRow is one object's state in the report.
type auditRow struct {
	digest string
	state  auditState
}

// auditReport is the full per-object state report plus a summary.
type auditReport struct {
	rows   []auditRow
	total  int
	counts map[auditState]int
}

// audit walks every stored object, marks the reachable set from HEAD, and
// classifies each object. It is read-only: it never deletes or rewrites.
// noVerify skips the integrity pass (a fast orphan scan without reading
// every object's bytes).
func (a *app) audit(ctx context.Context, noVerify bool) (*auditReport, error) {
	digests, err := a.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	reachable, err := a.reachableFromHead(ctx)
	if err != nil {
		return nil, err
	}
	rep := &auditReport{counts: make(map[auditState]int, 4)}
	verifier := cas.NewVerifier(a.backend, a.hasher)
	for _, h := range digests {
		key := h.String()
		reach := reachable[key]
		state := stateVerified
		if !noVerify {
			if err := verifier.Verify(ctx, h); err != nil {
				state = stateCorrupt // corruption outranks orphaned: report it first
			} else if !reach {
				state = stateOrphaned
			}
		} else if !reach {
			state = stateOrphaned // reachability needs no integrity check
		} else {
			state = stateUnverified
		}
		rep.rows = append(rep.rows, auditRow{digest: key, state: state})
		rep.counts[state]++
	}
	rep.total = len(digests)
	slices.SortFunc(rep.rows, func(a, b auditRow) int {
		if c := cmp.Compare(a.state, b.state); c != 0 {
			return c
		}
		return cmp.Compare(a.digest, b.digest)
	})
	return rep, nil
}

// reachableFromHead returns every object reachable from the HEAD commit by
// following References() through the gitlike object model. Without a HEAD the
// store has no roots and every object is unreachable — but only a genuinely
// absent HEAD means that: an unreadable or malformed HEAD is corruption and is
// reported instead of being treated as an empty root set.
//
// The closure is cas.Reachable, the core's root-seeded expansion: the ref
// lister resolves one object and hands back its references, and a digest that
// cannot be resolved (dangling or corrupt) simply ends that branch. The digest
// itself stays in the set, which is what lets the report show it as
// reachable-then-corrupt rather than silently dropping it.
func (a *app) reachableFromHead(ctx context.Context) (map[string]bool, error) {
	head, present, err := a.headCommitOrAbsent(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return map[string]bool{}, nil // no HEAD yet: no roots
	}
	res := gitlike.NewResolver(a.repo)
	refs := cas.RefListerFunc(func(ctx context.Context, d cas.Digest) ([]cas.Digest, error) {
		ro, err := res.ResolveAny(ctx, d)
		if err != nil {
			return nil, nil // dangling/corrupt: nothing more to walk from here
		}
		return ro.References(), nil
	})
	return cas.Reachable(ctx, refs, []cas.Digest{head})
}

// print writes one line per object (state, digest) followed by a summary.
func (r *auditReport) print(out io.Writer) {
	for _, row := range r.rows {
		fmt.Fprintf(out, "%-10s %s\n", row.state, row.digest)
	}
	fmt.Fprintf(out, "audit: %d objects — verified %d, orphaned %d, corrupt %d, unverified %d\n",
		r.total,
		r.counts[stateVerified],
		r.counts[stateOrphaned],
		r.counts[stateCorrupt],
		r.counts[stateUnverified],
	)
}
