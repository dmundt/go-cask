package cas

import "context"

// Walker traverses a single-type object graph via References() — the generic
// typed adapter over WalkDigests, so the core has one traversal implementation
// rather than two. It works for any object type with no knowledge of the domain
// model. Content addressing makes a cycle impossible in an honestly written
// store: an object's address is derived from its bytes, so no object can
// reference itself directly or transitively. Traversal nevertheless tracks the
// digests it has visited — a shared subgraph is walked once, not once per path,
// and a store written by another tool (a Backend does not re-verify the bytes
// it is handed) cannot make the walk loop — and uses an explicit stack instead
// of recursion, so a very deep graph terminates instead of exhausting the
// goroutine stack.
type Walker[T Object[T]] struct {
	store *Store[T]
	visit func(T) error
}

// NewWalker creates a walker over store that calls visit for every object
// reached.
func NewWalker[T Object[T]](store *Store[T], visit func(T) error) *Walker[T] {
	return &Walker[T]{store: store, visit: visit}
}

// Walk visits the object at d and then every object reachable through
// References(), depth first, visiting each digest at most once. It returns the
// first error from visit or from any read. A missing object returns
// ErrNotFound.
func (w *Walker[T]) Walk(ctx context.Context, d Digest) error {
	resolve := func(ctx context.Context, cur Digest) (Node, []Digest, error) {
		// A nil refs slice tells the walk to follow the node's own
		// References() — the typed adapter reads the object either way.
		obj, err := w.store.Get(ctx, cur)
		if err != nil {
			return nil, nil, err
		}
		return obj, nil, nil
	}
	return WalkDigests(ctx, resolve, []Digest{d}, func(_ Digest, node Node, _ []Digest) error {
		return w.visit(node.(T)) // the store returned a T, so this cannot fail
	})
}
