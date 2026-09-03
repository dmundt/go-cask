package main

import (
	"context"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Repository bundles the per-type stores over one RawStore — the app's own
// repository, copied from the gitlike pattern (cas-core §4.12).
type Repository struct {
	raw         cas.RawStore
	Notes       *cas.Store[*Note]
	Tags        *cas.Store[*Tag]
	Attachments *cas.Store[*Attachment]
}

func newRepository(raw cas.RawStore) (*Repository, error) {
	notes, err := cas.NewStore(raw, cas.JSONCodec[*Note]{}, "sha256")
	if err != nil {
		return nil, err
	}
	tags, err := cas.NewStore(raw, cas.JSONCodec[*Tag]{}, "sha256")
	if err != nil {
		return nil, err
	}
	attachments, err := cas.NewStore(raw, cas.JSONCodec[*Attachment]{}, "sha256")
	if err != nil {
		return nil, err
	}
	return &Repository{raw: raw, Notes: notes, Tags: tags, Attachments: attachments}, nil
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

func (r *Resolver) ResolveNote(ctx context.Context, h cas.Hash) (*Note, error) {
	return r.repo.Notes.Get(ctx, h)
}

func (r *Resolver) ResolveTag(ctx context.Context, h cas.Hash) (*Tag, error) {
	return r.repo.Tags.Get(ctx, h)
}

func (r *Resolver) ResolveAttachment(ctx context.Context, h cas.Hash) (*Attachment, error) {
	return r.repo.Attachments.Get(ctx, h)
}

// ResolveAny discovers the type from the self-describing envelope and
// dispatches to the matching typed resolver.
func (r *Resolver) ResolveAny(ctx context.Context, h cas.Hash) (*ResolvedObject, error) {
	rc, err := r.repo.raw.Get(ctx, h)
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
		n, err := r.ResolveNote(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "note", Note: n}, nil
	case "tag":
		t, err := r.ResolveTag(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "tag", Tag: t}, nil
	case "attachment":
		a, err := r.ResolveAttachment(ctx, h)
		if err != nil {
			return nil, err
		}
		return &ResolvedObject{Type: "attachment", Attachment: a}, nil
	default:
		return nil, fmt.Errorf("%w: %q", cas.ErrUnknownType, typ)
	}
}
