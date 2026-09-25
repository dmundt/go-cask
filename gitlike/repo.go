package gitlike

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	casrepo "github.com/dmundt/go-cask/cas/repo"
)

// The Resolver is cas/repo's Resolver: it resolves a digest to the concrete
// object, so cas/repo.Walk and cas/repo.Reachable traverse a gitlike repository
// with the same rules as any other typed object graph. The assertion makes the
// contract a compile-time fact rather than a doc comment.
var _ casrepo.Resolver = (*Resolver)(nil)

// Codecs is the serialization set a Repository is built with: one Codec[T] per
// object type. gitlike names no codec — a repository is codec-agnostic, and the
// caller supplies the four codecs (the JSON codec is the usual choice:
// Codecs{Blob: json.New[*Blob](), Tree: json.New[*Tree](), …}).
//
// The set is a struct rather than four constructor parameters so a caller can
// build it once, name it and reuse it, and so adding an object type stays a
// compile-time change at every construction site.
type Codecs struct {
	// Blob serializes Blob objects.
	Blob cas.Codec[*Blob]
	// Tree serializes Tree objects.
	Tree cas.Codec[*Tree]
	// Commit serializes Commit objects.
	Commit cas.Codec[*Commit]
	// Tag serializes Tag objects.
	Tag cas.Codec[*Tag]
}

// Repository bundles the per-type stores (blob, tree, commit, tag) over one
// Backend, the caller's Hasher and the caller's Codecs — cross-type access
// without any: each store is typed, so calling the wrong store is a
// compile-time error.
type Repository struct {
	backend cas.Backend
	// Blobs stores Blob objects.
	Blobs *cas.Store[*Blob]
	// Trees stores Tree objects.
	Trees *cas.Store[*Tree]
	// Commits stores Commit objects.
	Commits *cas.Store[*Commit]
	// Tags stores Tag objects.
	Tags *cas.Store[*Tag]
}

// NewRepository builds a Repository over backend with the caller's hasher and
// codecs. The repository names neither the hash algorithm nor the wire format:
// both are client seams (cas-core §4.2, §4.6).
func NewRepository(backend cas.Backend, hasher cas.Hasher, codecs Codecs) *Repository {
	return &Repository{
		backend: backend,
		Blobs:   cas.New(backend, codecs.Blob, hasher),
		Trees:   cas.New(backend, codecs.Tree, hasher),
		Commits: cas.New(backend, codecs.Commit, hasher),
		Tags:    cas.New(backend, codecs.Tag, hasher),
	}
}

// Close releases the backend's resources. The four stores share one Backend, so
// one store's Close closes it (Store.Close is idempotent and forwards to the
// backend's io.Closer when it has one — packfs flushes its active pack there);
// fs and mem hold no resources and report nil.
func (r *Repository) Close() error { return r.Blobs.Close() }

// ResolvedObject is the typed union returned by Resolver.ResolveAny — the
// alternative to any for "resolve whatever this hash points to". Exactly one
// of the fields is non-nil, matching Type.
type ResolvedObject struct {
	// Type identifies which union field is populated.
	Type string
	// Commit is populated when Type is TypeCommit.
	Commit *Commit
	// Tree is populated when Type is TypeTree.
	Tree *Tree
	// Blob is populated when Type is TypeBlob.
	Blob *Blob
	// Tag is populated when Type is TypeTag.
	Tag *Tag
}

// Resolver resolves digests to the right concrete type. Dedicated methods
// (ResolveCommit, ResolveTree, ResolveBlob, ResolveTag) are compile-time
// typed; ResolveAny discovers the type from the stored bytes.
type Resolver struct {
	repo *Repository
}

// NewResolver creates a Resolver over repo.
func NewResolver(repo *Repository) *Resolver {
	return &Resolver{repo: repo}
}

// ResolveCommit returns the commit at d, or ErrNotFound.
func (r *Resolver) ResolveCommit(ctx context.Context, d cas.Digest) (*Commit, error) {
	return r.repo.Commits.Get(ctx, d)
}

// ResolveTree returns the tree at d, or ErrNotFound.
func (r *Resolver) ResolveTree(ctx context.Context, d cas.Digest) (*Tree, error) {
	return r.repo.Trees.Get(ctx, d)
}

// ResolveBlob returns the blob at d, or ErrNotFound.
func (r *Resolver) ResolveBlob(ctx context.Context, d cas.Digest) (*Blob, error) {
	return r.repo.Blobs.Get(ctx, d)
}

// ResolveTag returns the tag at d, or ErrNotFound.
func (r *Resolver) ResolveTag(ctx context.Context, d cas.Digest) (*Tag, error) {
	return r.repo.Tags.Get(ctx, d)
}

// Resolve resolves d to its concrete object as a cas/repo.Object, discovering
// the type from the self-describing envelope. The method is what makes the
// Resolver satisfy cas/repo.Resolver, so cas/repo.Walk and cas/repo.Reachable
// run over a gitlike repository without gitlike implementing either walk
// itself — and so an app that grows past these four types can register its own
// types with a cas/repo.Registry and walk the same store.
//
// An object whose stored type this repository does not know is reported as
// cas.ErrUnknownType (the versioned name is compared, so a future "blob@2" is
// unknown rather than decoded as a blob). A stored envelope that does not parse
// is a different answer: it is damage, so it surfaces as cas.ErrCorrupt, never
// as an unknown type — a walk must not treat an object it cannot read as one it
// can safely skip.
func (r *Resolver) Resolve(ctx context.Context, d cas.Digest) (casrepo.Object, error) {
	// cas.HeaderType is the library's one bounded envelope-header read: a
	// prefix large enough for any realistic codec tag and type name, parsed by
	// cas.EnvelopeType, so the cost of resolution stays independent of the
	// object's size and this resolver carries no header reader of its own. It is
	// deliberately not cas.PeekType: the backends hand back a plain
	// io.ReadCloser (*os.File, bytes.Reader), which is not an io.ByteReader, so
	// PeekType's byte-at-a-time adapter would cost a read syscall per header
	// byte. The legacy rule that an absent major version reads as "@1"
	// (object-versioning §2) lives in the core parser either way.
	typ, err := cas.HeaderType(ctx, r.repo.backend, d)
	if err != nil {
		return nil, err
	}
	switch typ {
	case TypeBlob:
		return objectOrErr(r.repo.Blobs.Get(ctx, d))
	case TypeTree:
		return objectOrErr(r.repo.Trees.Get(ctx, d))
	case TypeCommit:
		return objectOrErr(r.repo.Commits.Get(ctx, d))
	case TypeTag:
		return objectOrErr(r.repo.Tags.Get(ctx, d))
	default:
		return nil, fmt.Errorf("gitlike: %w: %q", cas.ErrUnknownType, typ)
	}
}

// ResolveAny determines the object's type from the self-describing envelope and
// returns the typed union. It returns (nil, ErrUnknownType) for an object type
// this repository does not know.
//
// Only the envelope header is read to learn the type — never the payload — so
// resolving a large Blob costs one bounded read plus the store read that
// decodes it.
func (r *Resolver) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	obj, err := r.Resolve(ctx, d)
	if err != nil {
		return nil, err
	}
	return resolvedObjectOf(obj)
}

// resolvedObjectOf maps a resolved concrete object onto the typed union — the
// single place the four model types are turned into union fields, used by both
// ResolveAny and WalkGraph.
func resolvedObjectOf(obj casrepo.Object) (*ResolvedObject, error) {
	switch o := obj.(type) {
	case *Blob:
		return &ResolvedObject{Type: bareType(TypeBlob), Blob: o}, nil
	case *Tree:
		return &ResolvedObject{Type: bareType(TypeTree), Tree: o}, nil
	case *Commit:
		return &ResolvedObject{Type: bareType(TypeCommit), Commit: o}, nil
	case *Tag:
		return &ResolvedObject{Type: bareType(TypeTag), Tag: o}, nil
	default:
		return nil, fmt.Errorf("gitlike: %w: %q", cas.ErrUnknownType, obj.Type())
	}
}

// objectOrErr turns a typed store read into the interface result. A failed read
// yields a nil Object rather than an interface holding a nil pointer, so a
// caller's obj == nil test keeps meaning "no object".
func objectOrErr[T casrepo.Object](obj T, err error) (casrepo.Object, error) {
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// References returns the outgoing references of whichever union field is
// populated, so a caller walking the graph — or feeding the result to
// cas.RefListerFunc and cas.Reachable — does not repeat the type switch. A blob
// is a leaf, and a union with no field set has no references.
func (ro *ResolvedObject) References() []cas.Digest {
	switch {
	case ro.Commit != nil:
		return ro.Commit.References()
	case ro.Tree != nil:
		return ro.Tree.References()
	case ro.Tag != nil:
		return ro.Tag.References()
	default:
		return nil
	}
}

// PrintObject renders any resolved object to a one-line summary — a type
// switch, no reflection.
func PrintObject(o *ResolvedObject) string {
	switch o.Type {
	case "blob":
		return fmt.Sprintf("blob (%d bytes)", len(o.Blob.Data))
	case "tree":
		return fmt.Sprintf("tree (%d entries)", len(o.Tree.Entries))
	case "commit":
		return fmt.Sprintf("commit by %s: %s", o.Commit.Author, o.Commit.Message)
	case "tag":
		if o.Tag.Target.IsZero() {
			return fmt.Sprintf("tag %q -> <absent>", o.Tag.Name) // a tag may exist before its target
		}
		return fmt.Sprintf("tag %q -> %s", o.Tag.Name, o.Tag.Target.Prefix(8))
	default:
		return fmt.Sprintf("unknown type %q", o.Type)
	}
}

// WalkGraph traverses the whole object graph reachable from d, resolving every
// node with the resolver and calling visit for each, depth first. Each digest is
// visited at most once — the same rule as cas.Walker[T] — so a shared subgraph
// is walked once rather than once per path and the cost is linear in the number
// of objects: a diamond-shaped history of n levels costs n visits, not 2^n.
// The visited set also terminates on a store this library did not write: the
// Backend stores bytes without recomputing their digest, so a crafted store CAN
// hold a cycle even though an honestly written one cannot. The stack is
// explicit, so a deep history does not exhaust the goroutine stack.
//
// The walk itself is cas/repo.Walk: gitlike no longer implements its own
// traversal, so a gitlike repository and a cas/repo.Registry follow identical
// traversal rules (at-most-once, explicit stack, context checked per node,
// references followed through cas/repo.Object.References()).
//
// gitlike's walk is stricter than cas/repo.Walk about types: a digest whose
// stored type this repository does not know aborts the walk with
// cas.ErrUnknownType, where cas/repo.Walk would report the unknown object to
// visit and keep going. A reference that points at nothing aborts it with
// cas.ErrNotFound, which both walks agree on.
func WalkGraph(ctx context.Context, resolver *Resolver, d cas.Digest, visit func(*ResolvedObject) error) error {
	return casrepo.Walk(ctx, resolver, []cas.Digest{d}, func(_ cas.Digest, obj casrepo.Object) error {
		ro, err := resolvedObjectOf(obj)
		if err != nil {
			return err
		}
		return visit(ro)
	})
}
