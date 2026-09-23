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
	backend cas.Backend
	// Notes stores Note objects.
	Notes *cas.Store[*Note]
	// Tags stores Tag objects.
	Tags *cas.Store[*Tag]
	// Attachments stores Attachment objects.
	Attachments *cas.Store[*Attachment]
}

func newRepository(backend cas.Backend, hasher cas.Hasher) (*Repository, error) {
	return &Repository{
		backend:     backend,
		Notes:       cas.New(backend, jsoncodec.New[*Note](), hasher),
		Tags:        cas.New(backend, jsoncodec.New[*Tag](), hasher),
		Attachments: cas.New(backend, jsoncodec.New[*Attachment](), hasher),
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

// Resolver resolves any hash to the right concrete type.
type Resolver struct{ repo *Repository }

func newResolver(repo *Repository) *Resolver { return &Resolver{repo: repo} }

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

// envelopeHeaderLimit bounds the prefix read to learn an object's type. The
// envelope header is [version u8][uvarint typeLen][type] — a few dozen bytes for
// any realistic type name — so this is generous while keeping the read cost of
// resolution independent of the object's size (gitlike/repo.go uses the same
// limit).
const envelopeHeaderLimit = 1 << 10

// ResolveAny discovers the type from the self-describing envelope and
// dispatches to the matching typed resolver.
//
// Only the envelope header is read to learn the type — never the payload — so
// resolving a large Attachment costs one bounded read, not a copy of the object.
func (r *Resolver) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	rc, err := r.repo.backend.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	prefix, err := io.ReadAll(io.LimitReader(rc, envelopeHeaderLimit))
	if err != nil {
		_ = rc.Close() // the read error is the one worth reporting
		return nil, fmt.Errorf("notes: read object header for resolution: %w", err)
	}
	if err := rc.Close(); err != nil {
		return nil, fmt.Errorf("notes: close object header reader: %w", err)
	}
	typ, err := parseType(prefix)
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
