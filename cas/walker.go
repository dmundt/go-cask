package cas

import "context"

// Walker[T] traverses a single-type object graph via References() — the
// generic core's only graph primitive. It works for any object type with no
// knowledge of the domain model. Content addressing makes cycles impossible,
// so no visited set is needed.
type Walker[T Object[T]] struct {
	store *Store[T]
	visit func(Object[T]) error
}

// NewWalker creates a walker over store that calls visit for every object
// reached.
func NewWalker[T Object[T]](store *Store[T], visit func(Object[T]) error) *Walker[T] {
	return &Walker[T]{store: store, visit: visit}
}

// Walk visits the object at h and then recurses over every reference, depth
// first. It returns the first error from visit or from any read. A missing
// object returns ErrNotFound.
func (w *Walker[T]) Walk(ctx context.Context, h Hash) error {
	obj, err := w.store.Get(ctx, h)
	if err != nil {
		return err
	}
	if err := w.visit(obj); err != nil {
		return err
	}
	for _, ref := range obj.References() {
		if err := w.Walk(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}
