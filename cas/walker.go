package cas

import "context"

// Walker[T] traverses a single-type object graph via References() — the
// generic core's only graph primitive. It works for any object type with no
// knowledge of the domain model. Content addressing makes cycles impossible
// for a well-behaved hasher, but a registered HashFunc need not be injective,
// so traversal tracks visited hashes and uses an explicit stack instead of
// recursion: a cyclic or very deep graph terminates instead of exhausting the
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

// Walk visits the object at h and then every object reachable through
// References(), depth first, visiting each hash at most once. It returns the
// first error from visit or from any read. A missing object returns
// ErrNotFound.
func (w *Walker[T]) Walk(ctx context.Context, h Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	visited := make(map[string]bool)
	stack := []Hash{h}
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
