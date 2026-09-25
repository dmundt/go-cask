package main

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// This file covers the notes package's exported object-model surface — the
// registry's typed resolvers and the leaf objects' References — which the
// acceptance tests in main_test.go reach only through the note side of the
// graph.
//
// Deliberately left uncovered, with the reason:
//
//   - newRepository's three RegisterStore error returns (repo.go:41, 44, 47):
//     RegisterStore fails only on a nil store or a duplicate type name, and
//     newRepository passes a freshly built non-nil store under each of the
//     three distinct versioned names, so no input to it can fail.
//   - resolvedObjectOf's default branch (repo.go:124): the registry only ever
//     resolves the three types registered above, each of which has its own
//     case, so no digest reaches the default.
//   - demo and main (main.go): the demo builds its own store under the process
//     working directory and prints to stdout; it is the runnable program
//     itself, not logic a test can invoke without recreating main.
func TestResolveTagAndAttachment(t *testing.T) {
	ctx := context.Background()
	repo, res := newTestRepo(t)

	tag, err := repo.Tags.Put(ctx, &Tag{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := repo.Attachments.Put(ctx, &Attachment{Data: []byte("attachment bytes")})
	if err != nil {
		t.Fatal(err)
	}

	// ResolveTag reads the tag store; ResolveAttachment reads the attachment
	// store. Each returns the stored value, not a re-encoded copy.
	gotTag, err := res.ResolveTag(ctx, tag)
	if err != nil {
		t.Fatalf("ResolveTag(%s) = %v", tag, err)
	}
	if gotTag.Name != "work" {
		t.Fatalf("ResolveTag returned %+v, want name work", gotTag)
	}
	gotAtt, err := res.ResolveAttachment(ctx, att)
	if err != nil {
		t.Fatalf("ResolveAttachment(%s) = %v", att, err)
	}
	if string(gotAtt.Data) != "attachment bytes" {
		t.Fatalf("ResolveAttachment returned %q, want the stored bytes", gotAtt.Data)
	}

	// A digest written by another type is not a tag: the store compares the
	// envelope's stored type with the one its codec decodes and reports
	// ErrUnknownType, so a cross-type read is a typed error rather than a
	// wrongly shaped value. An address nothing stored is ErrNotFound.
	if _, err := res.ResolveTag(ctx, att); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("ResolveTag(attachment digest) = %v, want ErrUnknownType", err)
	}
	if _, err := res.ResolveAttachment(ctx, tag); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("ResolveAttachment(tag digest) = %v, want ErrUnknownType", err)
	}
	if _, err := res.ResolveTag(ctx, sha256.Of([]byte("stored nowhere"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveTag(absent) = %v, want ErrNotFound", err)
	}
}

// Leaf objects reference nothing: References() is nil for both Tag and
// Attachment, which is what lets a walk stop there and a GC root set stay
// minimal.
func TestLeafReferencesAreNil(t *testing.T) {
	tag := &Tag{Name: "leaf"}
	if refs := tag.References(); refs != nil {
		t.Fatalf("Tag.References() = %v, want nil", refs)
	}
	att := &Attachment{Data: []byte("leaf")}
	if refs := att.References(); refs != nil {
		t.Fatalf("Attachment.References() = %v, want nil", refs)
	}
}

// Note.References() flattens the three reference groups in order and drops
// absent (zero) digests, so a walk never chases an empty address.
func TestNoteReferencesFlattenAndSkipAbsent(t *testing.T) {
	tag := sha256.Of([]byte("tag"))
	att := sha256.Of([]byte("attachment"))
	rel := sha256.Of([]byte("related"))
	n := &Note{
		Title:       "n",
		Tags:        []cas.Digest{tag, nil},
		Attachments: []cas.Digest{att},
		Related:     []cas.Digest{rel},
	}
	refs := n.References()
	if len(refs) != 3 {
		t.Fatalf("References() = %d refs (%v), want 3", len(refs), refs)
	}
	for i, want := range []cas.Digest{tag, att, rel} {
		if !refs[i].Equal(want) {
			t.Fatalf("refs[%d] = %s, want %s (tags, then attachments, then related)", i, refs[i], want)
		}
	}

	empty := &Note{Title: "empty"}
	if refs := empty.References(); len(refs) != 0 {
		t.Fatalf("References() with no refs = %v, want empty", refs)
	}
}

// ResolveAny maps each of the three stored types onto its union field and
// reports the unversioned type name, so a caller switches on one string.
func TestResolveAnyUnionFields(t *testing.T) {
	ctx := context.Background()
	repo, res := newTestRepo(t)

	tag, err := repo.Tags.Put(ctx, &Tag{Name: "idea"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := repo.Attachments.Put(ctx, &Attachment{Data: []byte("data")})
	if err != nil {
		t.Fatal(err)
	}
	note, err := repo.Notes.Put(ctx, &Note{Title: "n", Tags: []cas.Digest{tag}, Attachments: []cas.Digest{att}})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		digest   cas.Digest
		wantType string
		check    func(t *testing.T, ro *ResolvedObject)
	}{
		{
			name:     "note",
			digest:   note,
			wantType: "note",
			check: func(t *testing.T, ro *ResolvedObject) {
				if ro.Note == nil || ro.Tag != nil || ro.Attachment != nil {
					t.Fatalf("note union = %+v, want only Note set", ro)
				}
			},
		},
		{
			name:     "tag",
			digest:   tag,
			wantType: "tag",
			check: func(t *testing.T, ro *ResolvedObject) {
				if ro.Tag == nil || ro.Tag.Name != "idea" || ro.Note != nil || ro.Attachment != nil {
					t.Fatalf("tag union = %+v, want only Tag set to idea", ro)
				}
			},
		},
		{
			name:     "attachment",
			digest:   att,
			wantType: "attachment",
			check: func(t *testing.T, ro *ResolvedObject) {
				if ro.Attachment == nil || string(ro.Attachment.Data) != "data" || ro.Note != nil || ro.Tag != nil {
					t.Fatalf("attachment union = %+v, want only Attachment set", ro)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ro, err := res.ResolveAny(ctx, tc.digest)
			if err != nil {
				t.Fatalf("ResolveAny = %v", err)
			}
			if ro.Type != tc.wantType {
				t.Fatalf("union type = %q, want %q", ro.Type, tc.wantType)
			}
			tc.check(t, ro)
		})
	}
}

// An absent digest cannot be resolved to any type.
func TestResolveAnyAbsentDigest(t *testing.T) {
	ctx := context.Background()
	_, res := newTestRepo(t)
	if _, err := res.ResolveAny(ctx, sha256.Of([]byte("never stored"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(absent) = %v, want ErrNotFound", err)
	}
}

// The registry's typed resolver and each per-type store agree: a digest stored
// by one type can never be read back as another, which is the compile-time
// safety the exported Notes/Tags/Attachments fields exist for.
func TestTypeStoresDoNotCrossRead(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	tag, err := repo.Tags.Put(ctx, &Tag{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Notes.Get(ctx, tag); err == nil {
		t.Fatal("the note store must not read a tag digest")
	}
	if _, err := repo.Attachments.Get(ctx, tag); err == nil {
		t.Fatal("the attachment store must not read a tag digest")
	}
}

// bareType strips the version suffix and leaves an unversioned name alone.
func TestBareType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "note@1", want: "note"},
		{in: "attachment@2", want: "attachment"},
		{in: "note", want: "note"},
	}
	for _, tc := range cases {
		if got := bareType(tc.in); got != tc.want {
			t.Fatalf("bareType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
