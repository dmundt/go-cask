// Audit command for the files example: reports every stored object's
// derived state — verified / orphaned / corrupt / unverified — computed
// from the existing maintenance operations, never persisted.
//
// Reachability is marked from HEAD (the store's only root): objects the
// commit graph cannot reach are orphaned (GC candidates). Integrity is
// checked per object with FSRawStore.Verify unless -no-verify is given,
// in which case reachable objects are simply "unverified".
package main

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/examples/gitlike"
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
	hash  string
	state auditState
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
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		return nil, err
	}
	reachable, err := a.reachableFromHead(ctx)
	if err != nil {
		return nil, err
	}
	rep := &auditReport{counts: make(map[auditState]int, 4)}
	for _, h := range hashes {
		key := h.String()
		reach := reachable[key]
		state := stateVerified
		if !noVerify {
			if err := a.raw.Verify(ctx, h); err != nil {
				state = stateCorrupt // corruption outranks orphaned: report it first
			} else if !reach {
				state = stateOrphaned
			}
		} else if !reach {
			state = stateOrphaned // reachability needs no integrity check
		} else {
			state = stateUnverified
		}
		rep.rows = append(rep.rows, auditRow{hash: key, state: state})
		rep.counts[state]++
	}
	rep.total = len(hashes)
	sort.Slice(rep.rows, func(i, j int) bool {
		if rep.rows[i].state != rep.rows[j].state {
			return rep.rows[i].state < rep.rows[j].state
		}
		return rep.rows[i].hash < rep.rows[j].hash
	})
	return rep, nil
}

// reachableFromHead marks every object reachable from the HEAD commit by
// following References() through the gitlike object model. Without a HEAD
// the store has no roots and every object is unreachable.
func (a *app) reachableFromHead(ctx context.Context) (map[string]bool, error) {
	head, err := a.headCommit()
	if err != nil || head == nil {
		return map[string]bool{}, nil // no roots yet
	}
	seen := make(map[string]bool)
	if err := a.markReachable(ctx, head, seen); err != nil {
		return nil, err
	}
	return seen, nil
}

// markReachable adds h and, recursively, every hash its object references.
// A hash that cannot be resolved (dangling or corrupt) stops that branch;
// it was already marked, so it is still reported reachable-then-corrupt.
func (a *app) markReachable(ctx context.Context, h cas.Hash, seen map[string]bool) error {
	if h == nil || seen[h.String()] {
		return nil
	}
	seen[h.String()] = true
	ro, err := gitlike.NewResolver(a.repo).ResolveAny(ctx, h)
	if err != nil {
		return nil // dangling/corrupt: nothing more to walk from here
	}
	for _, ref := range referencesOf(ro) {
		if err := a.markReachable(ctx, ref, seen); err != nil {
			return err
		}
	}
	return nil
}

// referencesOf returns the outgoing references of a resolved object — the
// example's own type switch (gitlike's is unexported; this uses only the
// public References() methods).
func referencesOf(ro *gitlike.ResolvedObject) []cas.Hash {
	switch {
	case ro.Blob != nil:
		return ro.Blob.References()
	case ro.Tree != nil:
		return ro.Tree.References()
	case ro.Commit != nil:
		return ro.Commit.References()
	case ro.Tag != nil:
		return ro.Tag.References()
	default:
		return nil
	}
}

// print writes one line per object (state, hash) followed by a summary.
func (r *auditReport) print(out io.Writer) {
	for _, row := range r.rows {
		fmt.Fprintf(out, "%-10s %s\n", row.state, row.hash)
	}
	fmt.Fprintf(out, "audit: %d objects — verified %d, orphaned %d, corrupt %d, unverified %d\n",
		r.total,
		r.counts[stateVerified],
		r.counts[stateOrphaned],
		r.counts[stateCorrupt],
		r.counts[stateUnverified],
	)
}
