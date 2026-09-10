package main

import (
	"context"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// Repository bundles the per-type stores over one Backend — the app's own
// repository, copied from the gitlike pattern (cas-core §4.12).
type Repository struct {
	raw         cas.Backend
	Notes       *cas.Store[*Note]
	Tags        *cas.Store[*Tag]
	Attachments *cas.Store[*Attachment]
}

func newRepository(raw cas.Backend, hasher cas.Hasher) (*Repository, error) {
	return &Repository{
		raw:         raw,
		Notes:       cas.New(raw, jsoncodec.New[*Note](), hasher),
		Tags:        cas.New(raw, jsoncodec.New[*Tag](), hasher),
		Attachments: cas.New(raw, jsoncodec.New[*Attachment](), hasher),
	}, nil
}

// ResolvedObject is the typed union returned by ResolveAny — no any.
type ResolvedObject struct {
	Type       string
	Note       *Note
	Tag        *Tag
	Attachment *Attachment
}

// Resolver resolves any hash to the right concrete type.
type Resolver struct{ repo *Repository }

func newResolver(repo *Repository) *Resolver { return &Resolver{repo: repo} }

func (r *Resolver) ResolveNote(ctx context.Context, d cas.Digest) (*Note, error) {
	return r.repo.Notes.Get(ctx, d)
}

func (r *Resolver) ResolveTag(ctx context.Context, d cas.Digest) (*Tag, error) {
	return r.repo.Tags.Get(ctx, d)
}

func (r *Resolver) ResolveAttachment(ctx context.Context, d cas.Digest) (*Attachment, error) {
	return r.repo.Attachments.Get(ctx, d)
}

// ResolveAny discovers the type from the self-describing envelope and
// dispatches to the matching typed resolver.
func (r *Resolver) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	rc, err := r.repo.raw.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("read object for resolution: %w", err)
	}
	typ, err := parseType(data)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "note":
		n, err := r.ResolveNote(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "note", Note: n}, nil
	case "tag":
		t, err := r.ResolveTag(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "tag", Tag: t}, nil
	case "attachment":
		a, err := r.ResolveAttachment(ctx, d)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "attachment", Attachment: a}, nil
	default:
		return nil, fmt.Errorf("%w: %q", cas.ErrUnknownType, typ)
	}
}
