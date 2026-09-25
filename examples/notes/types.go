// Package main implements the notes example: a document-graph app with its
// own object types (Note, Tag, Attachment) built directly on the generic cas
// core and resolved through a cas/repo registry — proving the "apps build their
// own object model on the supported core APIs" pattern, not using gitlike
// (examples spec §3.3).
//
// Notes reference tags and attachments by hash; cas/repo.Registry resolves a
// digest across all three types and cas/repo.Reachable expands a root to the
// whole cross-type set; attachments are large blobs loaded lazily via
// CachedObject[T]; SmartCache prefetch warms references; a deliberately
// dangling reference is detected and reported without crashing; the generic
// Walker[T] traverses the same-type related-note chain.
//
// Usage:
//
//	go run ./examples/notes
package main

import (
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Type names (versioned majors per object-versioning §6).
const (
	typeNote       = "note@1"
	typeTag        = "tag@1"
	typeAttachment = "attachment@1"
)

// Note references tags, attachments, and related notes by digest. A reference
// field is a cas.Digest: it renders itself as one hex string through
// encoding.TextMarshaler and needs no JSON code here (cas-core §4.2), and
// `omitempty` drops an empty group.
type Note struct {
	// Title is the note title.
	Title string `json:"title"`
	// Body is the note body.
	Body string `json:"body"`
	// Tags lists referenced tag digests.
	Tags []cas.Digest `json:"tags,omitempty"`
	// Attachments lists referenced attachment digests.
	Attachments []cas.Digest `json:"attachments,omitempty"`
	// Related lists referenced note digests.
	Related []cas.Digest `json:"related,omitempty"`
}

// Type returns the versioned note type name.
func (n *Note) Type() string { return typeNote }

// References returns non-zero tag, attachment, and related-note digests.
func (n *Note) References() []cas.Digest {
	refs := make([]cas.Digest, 0, len(n.Tags)+len(n.Attachments)+len(n.Related))
	for _, group := range [][]cas.Digest{n.Tags, n.Attachments, n.Related} {
		for _, d := range group {
			if !d.IsZero() {
				refs = append(refs, d)
			}
		}
	}
	return refs
}

// Tag is a leaf object a note references.
type Tag struct {
	// Name is the tag name.
	Name string `json:"name"`
}

// Type returns the versioned tag type name.
func (t *Tag) Type() string { return typeTag }

// References returns nil because tags are leaves.
func (t *Tag) References() []cas.Digest { return nil }

// Attachment is a large blob, loaded lazily on access.
type Attachment struct {
	// Data contains the attachment bytes.
	Data []byte `json:"data"`
}

// Type returns the versioned attachment type name.
func (a *Attachment) Type() string { return typeAttachment }

// References returns nil because attachments are leaves.
func (a *Attachment) References() []cas.Digest { return nil }

// bareType returns an object type's unversioned base name: "note" for typeNote.
// The model's Type() is versioned ("note@1"); the ResolvedObject union's Type
// field is the bare name its callers compare against.
//
// This is deliberately the same helper `gitlike/types.go` carries, as are the
// union and resolver below: this example exists to show an app building its own
// object model on cas/repo without `gitlike` (examples spec §3.3, rule 5), so
// importing the reference library would delete the example's teaching content.
// A repo-wide duplicate scan is expected to flag it (go-cask#386); the rule, not
// the scan, decides here.
func bareType(versioned string) string {
	base, _, _ := strings.Cut(versioned, "@")
	return base
}
