package cas

import "context"

// Walker[T] traverses a single-type object graph via References() — the
// generic core's only graph primitive. It works for any object type with no
// knowledge of the domain model. Content addressing (sha256, the core's one
// algorithm) makes a cycle impossible: an object's address is derived from its
// bytes, so no object can reference itself directly or transitively.
// Traversal nevertheless tracks the hashes it has visited — a shared subgraph
// is walked once, not once per path — and uses an explicit stack instead of
// recursion, so a very deep graph terminates instead of exhausting the
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
	if err := ctx.Err(); err != nil {
		return err
	}
	visited := make(map[string]bool)
	stack := []Digest{d}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur.String()] {
			continue
		}
		visited[cur.String()] = true
		obj, err := w.store.Get(ctx, cur)
		if err != nil {
			return err
		}
		if err := w.visit(obj); err != nil {
			return err
		}
		refs := obj.References()
		for i := len(refs) - 1; i >= 0; i-- { // push reversed: keep reference order
			stack = append(stack, refs[i])
		}
	}
	return nil
}
