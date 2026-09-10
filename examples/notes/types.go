// Package main implements the notes example: a document-graph app with its
// own object types (Note, Tag, Attachment) built directly on the generic cas
// core — proving the "apps build their own repository/resolver" pattern
// without using gitlike (examples spec §3.3).
//
// Notes reference tags and attachments by hash; attachments are large blobs
// loaded lazily via CachedObject[T]; SmartCache prefetch warms references;
// a deliberately dangling reference is detected and reported without
// crashing; the generic Walker[T] traverses the same-type related-note
// chain.
//
// Usage:
//
//	go run ./examples/notes
package main

import (
	"fmt"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// Type names (versioned majors per object-versioning §6).
const (
	typeNote       = "note@1"
	typeTag        = "tag@1"
	typeAttachment = "attachment@1"
)

// Note references tags, attachments, and related notes by hash. The reference
// fields are cas.HashRef: they serialize as "algo:hex" strings and validate on
// decode without any code here (cas-core §4.2), and `omitempty` drops an empty
// group.
type Note struct {
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	Tags        []cas.HashRef `json:"tags,omitempty"`
	Attachments []cas.HashRef `json:"attachments,omitempty"`
	Related     []cas.HashRef `json:"related,omitempty"`
}

func (n *Note) Type() string { return typeNote }

func (n *Note) References() []cas.Hash {
	refs := make([]cas.Hash, 0, len(n.Tags)+len(n.Attachments)+len(n.Related))
	for _, group := range [][]cas.HashRef{n.Tags, n.Attachments, n.Related} {
		for _, r := range group {
			if h := r.Hash(); h != nil {
				refs = append(refs, h)
			}
		}
	}
	return refs
}

// Tag is a leaf object a note references.
type Tag struct {
	Name string `json:"name"`
}

func (t *Tag) Type() string           { return typeTag }
func (t *Tag) References() []cas.Hash { return nil }

// Attachment is a large blob, loaded lazily on access.
type Attachment struct {
	Data []byte `json:"data"`
}

func (a *Attachment) Type() string           { return typeAttachment }
func (a *Attachment) References() []cas.Hash { return nil }

// parseType extracts the unversioned type name ("note", "tag", ...) from the
// stored TLV envelope bytes (see cas.EnvelopeFromBytes).
func parseType(data []byte) (string, error) {
	env, err := cas.EnvelopeFromBytes(data)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	base, _, _ := strings.Cut(env.Type, "@")
	if base == "" {
		return "", fmt.Errorf("%w: object missing type", cas.ErrUnknownType)
	}
	return base, nil
}
