package cas

import (
	"context"
	"fmt"
	"slices"
)

// RefLister reports the direct references of the object stored at d,
// letting a reachability walk run over an otherwise opaque byte store without
// the core needing to know a concrete object model. A typed layer that can
// list an object's References() (Object[T], a per-type Store[T], or a
// multi-type registry/resolver) satisfies this trivially; Reachable stays
// entirely at the Digest level so it has no dependency on any single type.
type RefLister interface {
	References(ctx context.Context, d Digest) ([]Digest, error)
}

// RefListerFunc adapts a plain function to a RefLister.
type RefListerFunc func(ctx context.Context, d Digest) ([]Digest, error)

// References calls f.
func (f RefListerFunc) References(ctx context.Context, d Digest) ([]Digest, error) {
	return f(ctx, d)
}

// Node is the non-generic view of Object[T]: any type that implements
// Object[T] for some T satisfies Node too, because References never mentions T.
// It is what WalkDigests follows, so a caller whose node type is not a
// cas.Object — cas/repo's cross-type Object, for instance — can still traverse
// with the core's walk instead of writing its own.
type Node interface {
	// References returns the digests this node points to; may be nil.
	References() []Digest
}

// NodeResolver reads one node of the graph for WalkDigests. It returns the node
// the walk follows and hands to the visitor, and optionally that node's outgoing
// references; a nil refs slice means "ask the node" (Node.References), which is
// what a resolver over a concrete Object[T] wants — returning them explicitly
// spares a caller whose node already carries them the second call. A non-nil
// error aborts the walk.
type NodeResolver func(ctx context.Context, d Digest) (node Node, refs []Digest, err error)

// WalkDigests visits every digest reachable from roots, depth first with an
// explicit stack, calling visit for each digest at most once. It is the core's
// single graph-traversal implementation: Walker[T] is its typed adapter and
// cas/repo.Walk is its cross-type one, so one visited set, one stack and one
// rule set serve every walk in the library — a shared subgraph costs one visit
// per object rather than one per path, and a very deep graph terminates instead
// of exhausting the goroutine stack.
//
// Content addressing makes a cycle impossible in an honestly written store: an
// object's address is derived from its bytes, so no object can reference itself
// directly or transitively. The visited set nevertheless terminates on a store
// this library did not write — a Backend stores bytes without re-verifying
// their digest — and the zero Digest means "no reference", never a missing
// object (cas-core §4.1), so absent references are skipped rather than resolved.
//
// visit receives the node resolve returned and that node's references, so a
// caller that needs the concrete value (a typed object, a resolver's union)
// gets it without a second read. Returning a non-nil error from visit aborts
// the walk with that error unwrapped, and so does a resolver error — the walk
// adds no context of its own, so a caller supplies the wrapping its own package
// name requires. The context is checked once per node.
func WalkDigests(ctx context.Context, resolve NodeResolver, roots []Digest, visit func(d Digest, node Node, refs []Digest) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	visited := make(map[string]struct{})
	stack := make([]Digest, 0, len(roots))
	for _, root := range roots {
		if !root.IsZero() {
			stack = append(stack, root)
		}
	}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		key := d.String()
		if _, ok := visited[key]; ok {
			continue
		}
		visited[key] = struct{}{}

		node, refs, err := resolve(ctx, d)
		if err != nil {
			return err
		}
		if refs == nil && node != nil {
			refs = node.References() // the resolver left it to the node
		}
		if err := visit(d, node, refs); err != nil {
			return err
		}
		for _, ref := range slices.Backward(refs) { // push reversed: keep reference order
			if ref.IsZero() {
				continue // an absent reference is not a missing object (cas-core §4.1)
			}
			if _, seen := visited[ref.String()]; !seen {
				stack = append(stack, ref)
			}
		}
	}
	return nil
}

// Reachable computes the complete, transitively-closed set of digests
// reachable from roots by repeatedly calling refs.References until no new
// digest is discovered. It visits each digest at most once and is safe for
// any acyclic or cyclic graph (a cycle only stops expansion early; content
// addressing makes a genuine self-reference impossible, but a store written
// by another tool is not re-verified before this walk runs).
//
// It is WalkDigests with a set-building visitor, so the byte-layer expansion
// and a typed cross-type walk (cas/repo.Reachable) follow one traversal rule
// set instead of two.
//
// This is the documented, correct way to build the reachable set that
// Backend.GC and Backend.Prune require: both take an already-expanded
// reachable set and never follow references themselves. Passing only
// entry-point roots to GC/Prune without first calling Reachable (or an
// equivalent typed walk) silently deletes anything those references.
func Reachable(ctx context.Context, refs RefLister, roots []Digest) (map[string]bool, error) {
	reachable := make(map[string]bool, len(roots)+1)
	resolve := func(ctx context.Context, d Digest) (Node, []Digest, error) {
		direct, err := refs.References(ctx, d)
		if err != nil {
			return nil, nil, fmt.Errorf("cas: reachable: expand %s: %w", d, err)
		}
		// The explicit list is what a byte-layer lister provides: it has no node
		// to ask, so the walk never dereferences one here.
		return nil, direct, nil
	}
	err := WalkDigests(ctx, resolve, roots, func(d Digest, _ Node, _ []Digest) error {
		reachable[d.String()] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reachable, nil
}
