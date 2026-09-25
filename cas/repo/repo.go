// Package repo promotes gitlike's example-only Codecs/Repository/Resolver/
// WalkGraph pattern into a supported, general-purpose package: a typed,
// cross-type object registry over any cas.Backend, plus the two operations a
// real consumer with a typed graph needs and previously had to hand-roll —
// Walk (visit every reachable object, of any registered type, exactly once)
// and Reachable (the correct root-set expansion Backend.GC/Backend.Prune
// require).
//
// The primitive underneath is not new: Object[T].References() is already "the
// single source of truth for traversal, preloading, and GC reachability"
// (cas/object.go). What was missing was a way to follow References() across
// several Store[T] values without the caller knowing every concrete type in
// advance — gitlike's Resolver.ResolveAny/WalkGraph do exactly this, but only
// for gitlike's own four fixed types, and only as an example a library
// consumer cannot import without pulling in a demo package. Registry
// generalizes the same pattern to any number of caller-defined types.
//
// A store registered with RegisterStore is read back typed with
// LookupStore[T], so no caller asserts a type or receives a nil store: a name
// nothing registered is an *UnknownTypeError (Unwrap() == cas.ErrUnknownType),
// the same typed error Resolve returns for an unknown object type. The stores
// themselves are built by the caller — package cas ships no NewJSON, because it
// must not import cas/codec — with a one-line constructor in the consumer's own
// package (cas.New, cas/store.go) or with the registry form this package shows
// in its README.
//
// See docs/specs/consistency.md §4 for the root-set model this package
// implements, and docs/specs/library-design.md for the stable-surface
// contract. Filed and specified as go-cask#136.
package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
)

// Object is the non-generic view of cas.Object[T]: any type that implements
// cas.Object[T] for some T satisfies Object too, because the interface's
// method set never mentions T. Resolve, Walk, and Reachable work at this
// level precisely so they can hand back values of several concrete object
// types through one interface, without any and without a runtime type
// registry beyond Registry's own type-name -> Decoder map.
type Object interface {
	// Type returns the versioned type name, e.g. "blob@1".
	Type() string
	// References returns the digests this object points to; may be nil.
	References() []cas.Digest
}

// Decoder resolves the object stored at d into its concrete Object. A typical
// Decoder is a thin closure over an existing *cas.Store[T] (see RegisterStore);
// backend is passed in for a Decoder that needs to read the backend directly,
// but most Decoders ignore it and use their own closed-over Store instead.
type Decoder func(ctx context.Context, backend cas.Backend, d cas.Digest) (Object, error)

// Resolver resolves a digest to its typed Object. Walk and Reachable depend on
// this interface rather than the concrete *Registry, so a test double can
// stand in for a Registry.
type Resolver interface {
	Resolve(ctx context.Context, d cas.Digest) (Object, error)
}

// Registry maps a versioned type name (Object.Type(), e.g. "blob@1") to the
// Decoder that can resolve it, so a digest can be resolved without the caller
// knowing its type in advance — the promotion of gitlike's Resolver.ResolveAny
// to any number of caller-defined types. Registry is safe for concurrent use.
type Registry struct {
	backend cas.Backend
	hasher  cas.Hasher

	mu       sync.RWMutex
	decoders map[string]Decoder
	stores   map[string]any
}

// NewRegistry creates an empty Registry over backend, validating every digest it
// is asked to resolve against hasher before touching the backend. Register (or
// RegisterStore) one Decoder per type name before calling Resolve/Walk/
// Reachable.
func NewRegistry(backend cas.Backend, hasher cas.Hasher) *Registry {
	return &Registry{
		backend:  backend,
		hasher:   hasher,
		decoders: make(map[string]Decoder),
		stores:   make(map[string]any),
	}
}

// Register adds decode for typeName. It fails at construction time — not at
// first use as a nil-decoder panic — when typeName is empty, decode is nil, or
// typeName is already registered; the error names typeName in both cases so a
// caller sees exactly which type collided.
func (r *Registry) Register(typeName string, decode Decoder) error {
	if typeName == "" {
		return errors.New("cas/repo: register: empty type name")
	}
	if decode == nil {
		return fmt.Errorf("cas/repo: register %q: nil decoder", typeName)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.decoders[typeName]; exists {
		return fmt.Errorf("cas/repo: register %q: %q is already registered", typeName, typeName)
	}
	r.decoders[typeName] = decode
	return nil
}

// RegisterStore registers store under typeName, deriving its Decoder from
// store.Get. typeName MUST equal the exact string every value store holds
// returns from Type(): a mismatch means those objects are permanently
// "unknown type" to this Registry (UnknownTypeError), not a panic. The store
// itself is also recorded so a later LookupStore[T] call can hand it back
// typed.
//
// RegisterStore is a free function, not a Registry method, because Go method
// type parameters cannot be instantiated per call the way a generic function
// can: RegisterStore[T](reg, typeName, store) lets each call name its own T.
func RegisterStore[T cas.Object[T]](r *Registry, typeName string, store *cas.Store[T]) error {
	if store == nil {
		return fmt.Errorf("cas/repo: register store %q: nil store", typeName)
	}
	decode := func(ctx context.Context, _ cas.Backend, d cas.Digest) (Object, error) {
		obj, err := store.Get(ctx, d)
		if err != nil {
			return nil, err
		}
		return obj, nil
	}
	if err := r.Register(typeName, decode); err != nil {
		return err
	}
	r.mu.Lock()
	r.stores[typeName] = store
	r.mu.Unlock()
	return nil
}

// LookupStore returns the *cas.Store[T] that RegisterStore recorded under
// typeName, so a caller that needs typed operations beyond Resolve/Walk (Put,
// Get, Delete on a known type) reaches the store it registered without a type
// assertion. The registry's type-erased map stays private: the only value
// LookupStore hands back is already the caller's T, and no caller ever
// receives a nil store or a failed assertion.
//
// LookupStore is a free function, not a Registry method, for the same reason
// RegisterStore is: a method cannot name its own T per call.
//
// A typeName nothing registered — including one registered through the
// lower-level Register, which records no store — returns an *UnknownTypeError
// (Unwrap() == cas.ErrUnknownType), the same typed error Resolve returns for an
// unknown type. A typeName registered under a different T returns an error
// naming both the registered and the requested store type.
func LookupStore[T cas.Object[T]](r *Registry, typeName string) (*cas.Store[T], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.stores[typeName]
	if !ok {
		return nil, &UnknownTypeError{TypeName: typeName}
	}
	store, ok := registered.(*cas.Store[T])
	if !ok {
		var requested *cas.Store[T] // %T of the zero instantiation names the requested type
		return nil, fmt.Errorf("cas/repo: lookup store %q: registered as %T, requested %T", typeName, registered, requested)
	}
	return store, nil
}

// Resolve determines the object's type from its self-describing envelope and
// dispatches to the registered Decoder. It returns an *UnknownTypeError
// (Unwrap() == cas.ErrUnknownType) for a type with no registered Decoder —
// never a nil dereference — so a caller can tell "this digest names a type I
// don't know" apart from "this digest does not exist" (cas.ErrNotFound) or any
// other Decoder failure.
//
// An envelope that does not parse is not a dispatch miss: the bytes are damaged,
// so Resolve reports cas.ErrCorrupt (wrapped from cas.EnvelopeType) and no
// *UnknownTypeError. A caller that treats an unknown type as "skip it" can
// therefore never skip an object it merely cannot read.
func (r *Registry) Resolve(ctx context.Context, d cas.Digest) (Object, error) {
	if err := cas.CheckDigest(d, "cas/repo: resolve"); err != nil {
		return nil, err
	}
	if err := r.hasher.Validate(d); err != nil {
		return nil, fmt.Errorf("cas/repo: resolve: %w", err)
	}
	typeName, err := cas.HeaderType(ctx, r.backend, d)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	decode, ok := r.decoders[typeName]
	r.mu.RUnlock()
	if !ok {
		return nil, &UnknownTypeError{Digest: d, TypeName: typeName}
	}
	obj, err := decode(ctx, r.backend, d)
	if err != nil {
		return nil, fmt.Errorf("cas/repo: resolve %s (%s): %w", d, typeName, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("cas/repo: resolve %s: decoder for %q returned a nil object", d, typeName)
	}
	return obj, nil
}

// UnknownObject is the placeholder Walk passes to visit for a digest whose
// envelope names a type with no registered Decoder. Its References is always
// nil: Walk has no way to know what such an object points to, so that branch
// of the graph ends here without being followed further — but Walk itself
// does not abort, matching the acceptance criterion that an unknown type is
// reported (so a maintenance pass such as verify can name it), not a failure
// that stops the whole walk.
type UnknownObject struct {
	// Digest is the object's address.
	Digest cas.Digest
	// TypeName is the versioned type name read from the envelope.
	TypeName string
	// Err is the resolve error; errors.Is(Err, cas.ErrUnknownType) is always true.
	Err error
}

// Type returns TypeName.
func (u *UnknownObject) Type() string { return u.TypeName }

// References always returns nil: an unknown type's references cannot be read.
func (u *UnknownObject) References() []cas.Digest { return nil }

// Walk traverses every digest reachable from roots via res, calling visit for
// each digest exactly once, depth-first with an explicit stack — the same
// guarantee gitlike's WalkGraph documents, so a deep object graph does not
// exhaust the goroutine stack. It follows Object.References() across however
// many distinct types res knows how to resolve, which is what makes it the
// cross-type successor to the single-type cas.Walker[T]. The traversal itself
// is cas.WalkDigests: this function owns only what is specific to a
// registry-backed walk — resolving a digest to its registered type and
// classifying the failure — so a typed walk and a byte-layer one share one rule
// set (at-most-once, explicit stack, context checked per node, references
// followed in order).
//
// Two failure modes are handled differently, matching go-cask#136's
// acceptance criteria:
//
//   - A digest whose type has no registered Decoder is reported to visit as
//     an *UnknownObject, and Walk keeps going with the rest of the queue: an
//     unknown type does not abort the walk.
//   - Any other Resolve failure — most commonly cas.ErrNotFound for a
//     reference that points at nothing, or cas.ErrCorrupt for a stored
//     envelope that does not parse — aborts Walk immediately and is returned
//     as the resolver's own wrapped error: a broken reference is a named
//     error, not something the walk silently stops on or skips past. Damage in
//     particular is never reported as an *UnknownObject: an object Walk cannot
//     read is not a type it merely does not know.
//
// visit may itself return a non-nil error (including for an *UnknownObject) to
// abort Walk early; that error is returned unwrapped.
func Walk(ctx context.Context, res Resolver, roots []cas.Digest, visit func(d cas.Digest, obj Object) error) error {
	resolve := func(ctx context.Context, d cas.Digest) (cas.Node, []cas.Digest, error) {
		return resolveNode(ctx, res, d)
	}
	return cas.WalkDigests(ctx, resolve, roots, func(d cas.Digest, node cas.Node, _ []cas.Digest) error {
		return visit(d, node.(Object)) // resolveNode only ever returns an Object
	})
}

// resolveNode reads d through res and classifies the outcome for the walk: a
// successfully decoded object (whose references the walk reads from the node),
// or an *UnknownObject, which is reported to visit and is a leaf because an
// unknown type's references cannot be read — the resolve error travels in the
// node's Err field rather than as this function's return, because an unknown
// type must not abort the walk. Any other failure is returned for the walk to
// abort on, exactly as the resolver wrapped it.
func resolveNode(ctx context.Context, res Resolver, d cas.Digest) (cas.Node, []cas.Digest, error) {
	obj, err := res.Resolve(ctx, d)
	if err == nil {
		return obj, nil, nil
	}
	var ute *UnknownTypeError
	if errors.As(err, &ute) {
		return &UnknownObject{Digest: d, TypeName: ute.TypeName, Err: err}, nil, nil
	}
	return nil, nil, err
}

// Reachable computes the complete, transitively-closed set of digests
// reachable from roots via res — the typed, cross-type counterpart of
// cas.Reachable, built on Walk so it inherits Walk's tolerance of unregistered
// types (an unknown-type digest is included in the reachable set, since it
// must not be deleted, even though its own references cannot be followed) and
// its abort-on-broken-reference behavior (a dangling reference fails
// Reachable rather than silently under-counting the reachable set).
//
// This is the documented, correct way to build the argument
// Backend.GC/Backend.Prune require for a typed, multi-type object graph:
// passing only entry-point roots without first expanding through Reachable (or
// Walk) silently deletes anything those roots reference.
func Reachable(ctx context.Context, res Resolver, roots []cas.Digest) (map[string]bool, error) {
	reachable := make(map[string]bool, len(roots)+1)
	err := Walk(ctx, res, roots, func(d cas.Digest, _ Object) error {
		reachable[d.String()] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reachable, nil
}
