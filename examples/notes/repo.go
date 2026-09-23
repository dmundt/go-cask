package main

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	casrepo "github.com/dmundt/go-cask/cas/repo"
)

// Repository bundles the app's per-type stores over one Backend and the
// cas/repo.Registry that resolves a digest to the right concrete type across
// them (cas-core §4.12, examples spec §3.3).
//
// The registry is what an app no longer copies from gitlike: RegisterStore
// records each typed store under its type name, and Registry.Resolve reads that
// name from the stored envelope, so cross-type resolution is a core API rather
// than a hand-written header read and type switch. The typed stores stay
// exported fields, so calling the wrong store is still a compile-time error.
type Repository struct {
	registry *casrepo.Registry
	// Notes stores Note objects.
	Notes *cas.Store[*Note]
	// Tags stores Tag objects.
	Tags *cas.Store[*Tag]
	// Attachments stores Attachment objects.
	Attachments *cas.Store[*Attachment]
}

// newRepository builds the per-type stores over backend with the app's codecs
// and registers each one, so a digest can be resolved without the caller
// knowing its type in advance.
func newRepository(backend cas.Backend, hasher cas.Hasher) (*Repository, error) {
	notes := cas.New(backend, jsoncodec.New[*Note](), hasher)
	tags := cas.New(backend, jsoncodec.New[*Tag](), hasher)
	attachments := cas.New(backend, jsoncodec.New[*Attachment](), hasher)

	registry := casrepo.NewRegistry(backend, hasher)
	if err := casrepo.RegisterStore(registry, typeNote, notes); err != nil {
		return nil, err
	}
	if err := casrepo.RegisterStore(registry, typeTag, tags); err != nil {
		return nil, err
	}
	if err := casrepo.RegisterStore(registry, typeAttachment, attachments); err != nil {
		return nil, err
	}

	return &Repository{
		registry:    registry,
		Notes:       notes,
		Tags:        tags,
		Attachments: attachments,
	}, nil
}

// ResolvedObject is the typed union returned by ResolveAny — no any.
type ResolvedObject struct {
	// Type identifies which union field is populated.
	Type string
	// Note is populated for note objects.
	Note *Note
	// Tag is populated for tag objects.
	Tag *Tag
	// Attachment is populated for attachment objects.
	Attachment *Attachment
}

// Resolver resolves any digest to the right concrete type through the app's
// registry.
type Resolver struct{ repo *Repository }

// The Resolver is cas/repo's Resolver, so cas/repo.Walk, cas/repo.Reachable and
// any other cross-type helper in that package run over this app's object graph.
var _ casrepo.Resolver = (*Resolver)(nil)

func newResolver(repo *Repository) *Resolver { return &Resolver{repo: repo} }

// Resolve resolves d to its concrete object, discovering the type from the
// self-describing envelope through the registry.
func (r *Resolver) Resolve(ctx context.Context, d cas.Digest) (casrepo.Object, error) {
	return r.repo.registry.Resolve(ctx, d)
}

// ResolveNote loads a note by digest.
func (r *Resolver) ResolveNote(ctx context.Context, d cas.Digest) (*Note, error) {
	return r.repo.Notes.Get(ctx, d)
}

// ResolveTag loads a tag by digest.
func (r *Resolver) ResolveTag(ctx context.Context, d cas.Digest) (*Tag, error) {
	return r.repo.Tags.Get(ctx, d)
}

// ResolveAttachment loads an attachment by digest.
func (r *Resolver) ResolveAttachment(ctx context.Context, d cas.Digest) (*Attachment, error) {
	return r.repo.Attachments.Get(ctx, d)
}

// ResolveAny discovers the type from the self-describing envelope and returns
// the typed union. Only the envelope header is read to learn the type — never
// the payload — so resolving a large Attachment costs one bounded read plus the
// store read that decodes it.
func (r *Resolver) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	obj, err := r.Resolve(ctx, d)
	if err != nil {
		return nil, err
	}
	return resolvedObjectOf(obj)
}

// resolvedObjectOf maps a resolved concrete object onto the typed union — the
// one place the app's three model types become union fields.
func resolvedObjectOf(obj casrepo.Object) (*ResolvedObject, error) {
	switch o := obj.(type) {
	case *Note:
		return &ResolvedObject{Type: bareType(typeNote), Note: o}, nil
	case *Tag:
		return &ResolvedObject{Type: bareType(typeTag), Tag: o}, nil
	case *Attachment:
		return &ResolvedObject{Type: bareType(typeAttachment), Attachment: o}, nil
	default:
		return nil, fmt.Errorf("notes: %w: %q", cas.ErrUnknownType, obj.Type())
	}
}
