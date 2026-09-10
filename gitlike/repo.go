package gitlike

import (
	"context"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Codecs is the serialization set a Repository is built with: one Codec[T] per
// object type. gitlike names no codec — a repository is codec-agnostic, and the
// caller supplies the four codecs (the JSON codec is the usual choice:
// Codecs{Blob: json.New[*Blob](), Tree: json.New[*Tree](), …}).
//
// The set is a struct rather than four constructor parameters so a caller can
// build it once, name it and reuse it, and so adding an object type stays a
// compile-time change at every construction site.
type Codecs struct {
	Blob   cas.Codec[*Blob]
	Tree   cas.Codec[*Tree]
	Commit cas.Codec[*Commit]
	Tag    cas.Codec[*Tag]
}

// Repository bundles the per-type stores (blob, tree, commit, tag) over one
// Backend, the caller's Hasher and the caller's Codecs — cross-type access
// without any: each store is typed, so calling the wrong store is a
// compile-time error.
type Repository struct {
	raw     cas.Backend
	Blobs   *cas.Store[*Blob]
	Trees   *cas.Store[*Tree]
	Commits *cas.Store[*Commit]
	Tags    *cas.Store[*Tag]
}

// NewRepository builds a Repository over raw with the caller's hasher and
// codecs. The repository names neither the hash algorithm nor the wire format:
// both are client seams (cas-core §4.2, §4.6).
func NewRepository(raw cas.Backend, hasher cas.Hasher, codecs Codecs) *Repository {
	return &Repository{
		raw:     raw,
		Blobs:   cas.New(raw, codecs.Blob, hasher),
		Trees:   cas.New(raw, codecs.Tree, hasher),
		Commits: cas.New(raw, codecs.Commit, hasher),
		Tags:    cas.New(raw, codecs.Tag, hasher),
	}
}

// ResolvedObject is the typed union returned by Resolver.ResolveAny — the
// alternative to any for "resolve whatever this hash points to". Exactly one
// of the fields is non-nil, matching Type.
type ResolvedObject struct {
	Type   string
	Commit *Commit
	Tree   *Tree
	Blob   *Blob
	Tag    *Tag
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

// ResolveAny determines the object's type from the self-describing envelope
// and dispatches to the matching typed resolver. It returns
// (nil, ErrUnknownType) for an object type this repository does not know.
func (r *Resolver) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	rc, err := r.repo.raw.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("gitlike: read object for resolution: %w", err)
	}
	typ, err := parseType(data)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "blob":
		blob, err := r.ResolveBlob(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "blob", Blob: blob}, nil
	case "tree":
		tree, err := r.ResolveTree(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "tree", Tree: tree}, nil
	case "commit":
		commit, err := r.ResolveCommit(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "commit", Commit: commit}, nil
	case "tag":
		tag, err := r.ResolveTag(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "tag", Tag: tag}, nil
	default:
		return nil, fmt.Errorf("gitlike: %w: %q", cas.ErrUnknownType, typ)
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
		return fmt.Sprintf("tag %q -> %s", o.Tag.Name, shortDigest(o.Tag.Target))
	default:
		return fmt.Sprintf("unknown type %q", o.Type)
	}
}

// shortDigest renders the first 8 hex chars of a digest for display (the
// viewer's short-digest default). A digest shorter than 8 hex chars (a client
// hasher may produce one — the core names no algorithm) is rendered whole
// instead of being sliced out of range.
func shortDigest(d cas.Digest) string {
	if d.IsZero() {
		return "<absent>"
	}
	s := d.String()
	if len(s) <= 8 {
		return s
	}
	return s[:8]
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
func WalkGraph(ctx context.Context, resolver *Resolver, d cas.Digest, visit func(*ResolvedObject) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	visited := make(map[string]bool)
	stack := []cas.Digest{d}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.IsZero() || visited[cur.String()] {
			continue
		}
		visited[cur.String()] = true
		ro, err := resolver.ResolveAny(ctx, cur)
		if err != nil {
			return err
		}
		if err := visit(ro); err != nil {
			return err
		}
		refs := referencesOf(ro)
		for i := len(refs) - 1; i >= 0; i-- { // push reversed: keep reference order
			stack = append(stack, refs[i])
		}
	}
	return nil
}

// referencesOf returns the outgoing references of a resolved object.
func referencesOf(ro *ResolvedObject) []cas.Digest {
	switch ro.Type {
	case "commit":
		return ro.Commit.References()
	case "tree":
		return ro.Tree.References()
	case "tag":
		return ro.Tag.References()
	default:
		return nil
	}
}
