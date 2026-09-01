package gitlike

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Repository bundles the per-type stores (blob, tree, commit, tag) over one
// RawStore and one hash algorithm — cross-type access without any: each
// store is typed, so calling the wrong store is a compile-time error.
type Repository struct {
	raw     cas.RawStore
	Blobs   *cas.Store[*Blob]
	Trees   *cas.Store[*Tree]
	Commits *cas.Store[*Commit]
	Tags    *cas.Store[*Tag]
}

// NewRepository builds a Repository over raw with the given hash algorithm.
// It returns ErrUnknownAlgorithm if algo is not registered.
func NewRepository(raw cas.RawStore, algo string) (*Repository, error) {
	blobs, err := cas.NewStore(raw, cas.JSONCodec[*Blob]{}, algo)
	if err != nil {
		return nil, err
	}
	trees, err := cas.NewStore(raw, cas.JSONCodec[*Tree]{}, algo)
	if err != nil {
		return nil, err
	}
	commits, err := cas.NewStore(raw, cas.JSONCodec[*Commit]{}, algo)
	if err != nil {
		return nil, err
	}
	tags, err := cas.NewStore(raw, cas.JSONCodec[*Tag]{}, algo)
	if err != nil {
		return nil, err
	}
	return &Repository{raw: raw, Blobs: blobs, Trees: trees, Commits: commits, Tags: tags}, nil
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

// Resolver resolves hashes to the right concrete type. Dedicated methods
// (ResolveCommit, ResolveTree, ResolveBlob, ResolveTag) are compile-time
// typed; ResolveAny discovers the type from the stored bytes.
type Resolver struct {
	repo *Repository
}

// NewResolver creates a Resolver over repo.
func NewResolver(repo *Repository) *Resolver {
	return &Resolver{repo: repo}
}

// ResolveCommit returns the commit at h, or ErrNotFound.
func (r *Resolver) ResolveCommit(ctx context.Context, h cas.Hash) (*Commit, error) {
	return r.repo.Commits.GetTyped(ctx, h)
}

// ResolveTree returns the tree at h, or ErrNotFound.
func (r *Resolver) ResolveTree(ctx context.Context, h cas.Hash) (*Tree, error) {
	return r.repo.Trees.GetTyped(ctx, h)
}

// ResolveBlob returns the blob at h, or ErrNotFound.
func (r *Resolver) ResolveBlob(ctx context.Context, h cas.Hash) (*Blob, error) {
	return r.repo.Blobs.GetTyped(ctx, h)
}

// ResolveTag returns the tag at h, or ErrNotFound.
func (r *Resolver) ResolveTag(ctx context.Context, h cas.Hash) (*Tag, error) {
	return r.repo.Tags.GetTyped(ctx, h)
}

// ResolveAny determines the object's type from the self-describing envelope
// and dispatches to the matching typed resolver. It returns
// (nil, ErrUnknownType) for an object type this repository does not know.
func (r *Resolver) ResolveAny(ctx context.Context, h cas.Hash) (*ResolvedObject, error) {
	rc, err := r.repo.raw.Get(ctx, h)
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
		blob, err := r.ResolveBlob(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "blob", Blob: blob}, nil
	case "tree":
		tree, err := r.ResolveTree(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "tree", Tree: tree}, nil
	case "commit":
		commit, err := r.ResolveCommit(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "commit", Commit: commit}, nil
	case "tag":
		tag, err := r.ResolveTag(ctx, h)
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
		return fmt.Sprintf("tag %q -> %s", o.Tag.Name, shortHash(o.Tag.Target))
	default:
		return fmt.Sprintf("unknown type %q", o.Type)
	}
}

// shortHash renders the first 8 hex chars of a digest for display (the
// viewer's short-hash default).
func shortHash(h cas.Hash) string {
	if h == nil {
		return "<nil>"
	}
	s := h.String()
	_, hexPart, _ := strings.Cut(s, ":")
	if len(hexPart) > 8 {
		return hexPart[:8]
	}
	return hexPart
}

// WalkGraph traverses the whole object graph reachable from h, resolving
// every node with the resolver and calling visit for each. The type-switch
// dispatch makes it specific to this object set (the generic alternative is
// cas.Walker[T]). Content addressing makes cycles impossible, so no visited
// set is needed.
func WalkGraph(ctx context.Context, resolver *Resolver, h cas.Hash, visit func(*ResolvedObject) error) error {
	ro, err := resolver.ResolveAny(ctx, h)
	if err != nil {
		return err
	}
	if err := visit(ro); err != nil {
		return err
	}
	for _, ref := range referencesOf(ro) {
		if err := WalkGraph(ctx, resolver, ref, visit); err != nil {
			return err
		}
	}
	return nil
}

// referencesOf returns the outgoing references of a resolved object.
func referencesOf(ro *ResolvedObject) []cas.Hash {
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
