// Package gitlike implements the reference object model for the cas core: a
// Git-like Blob/Tree/Commit/Tag model layered on the generic Store[T]
// primitives (cas-core §4.12). It is a reference/copy-source library — apps
// build their own Object[T] types and repository/resolver combinations; gitlike
// is NOT part of the cas core and is excluded from its stable surface
// (cas-core §7.1).
//
// Every object type is versioned from the start (blob@1, tree@1, commit@1,
// tag@1); Store.Put stores each object in the core's self-describing TLV
// envelope [version u8][uvarint typeLen][type][uvarint payloadLen][payload]
// (cas-core §8 decision 1) around the codec payload.
//
// The package also provides Repository (per-type stores over one Backend),
// Resolver/ResolvedObject (cross-type resolution without any), WalkGraph
// (whole-graph traversal), CachedRepository (per-type LRU caches) and
// Preloader (background commit preloading).
package gitlike

import (
	"fmt"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// Object type names (versioned majors, object-versioning §6).
const (
	TypeBlob   = "blob@1"
	TypeTree   = "tree@1"
	TypeCommit = "commit@1"
	TypeTag    = "tag@1"
)

// Blob is a leaf object holding raw bytes.
type Blob struct {
	Data []byte `json:"data"`
}

// Type returns the versioned type name "blob@1".
func (b *Blob) Type() string { return TypeBlob }

// References returns nil — a blob is a leaf.
func (b *Blob) References() []cas.Digest { return nil }

// TreeEntry is one entry in a Tree. It is an entry, not an object itself;
// Hash references the stored object for Name and may be absent.
type TreeEntry struct {
	Name string     `json:"name"`
	Hash cas.Digest `json:"hash,omitzero"` // optional: absent is omitted
	Mode string     `json:"mode"`
}

// Validate reports whether the entry can be stored and read back: a tree entry
// must be named. An absent Hash is valid — an entry without a reference is
// representable and round-trips as absent. The store enforces this on every Put
// and Get (cas.Validator), so a nameless entry can neither be written nor read
// back; Tree.Validate runs the check over a whole tree in one call.
func (e TreeEntry) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("gitlike: tree entry has no name")
	}
	return nil
}

// Tree is a directory-like object referencing other objects by hash.
type Tree struct {
	Entries []TreeEntry `json:"entries"`
}

// Type returns the versioned type name "tree@1".
func (t *Tree) Type() string { return TypeTree }

// Validate reports whether every entry can be stored and read back. The store
// enforces it on Put and Get (cas.Validator); calling it directly lets a caller
// check a hand-built tree before Put and get the offending entry's index.
func (t *Tree) Validate() error {
	for i, e := range t.Entries {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
	}
	return nil
}

// References returns the digests of every entry.
func (t *Tree) References() []cas.Digest {
	refs := make([]cas.Digest, 0, len(t.Entries))
	for _, e := range t.Entries {
		if !e.Hash.IsZero() {
			refs = append(refs, e.Hash)
		}
	}
	return refs
}

// Commit points at a tree (and optionally a parent commit); an absent Parent
// marks a root commit.
type Commit struct {
	Tree    cas.Digest `json:"tree"`            // required: a missing/empty/null tree fails decode
	Parent  cas.Digest `json:"parent,omitzero"` // optional: absent is omitted
	Author  string     `json:"author"`
	Message string     `json:"message"`
	Time    time.Time  `json:"time"`
}

// Validate reports whether the commit can be stored and read back: a commit
// must name a tree. The store enforces it on every Put and Get (cas.Validator),
// so a tree-less commit cannot be written and a stored one is reported as
// ErrCorrupt rather than coming back as a rootless commit; calling Validate
// directly lets a caller check a hand-built object before Put.
//
// This rule lives here, not in a codec, so it holds whichever Codec[*Commit] a
// repository is built with.
func (c *Commit) Validate() error {
	if c.Tree.IsZero() {
		return fmt.Errorf("gitlike: commit has no tree")
	}
	return nil
}

// Type returns the versioned type name "commit@1".
func (c *Commit) Type() string { return TypeCommit }

// References returns the tree digest and the parent digest (if any).
func (c *Commit) References() []cas.Digest {
	refs := make([]cas.Digest, 0, 2)
	if !c.Tree.IsZero() {
		refs = append(refs, c.Tree)
	}
	if !c.Parent.IsZero() {
		refs = append(refs, c.Parent)
	}
	return refs
}

// Tag names a target object (typically a commit).
type Tag struct {
	Name    string     `json:"name"`
	Target  cas.Digest `json:"target"` // may be absent; a plain field keeps the historical ""
	Tagger  string     `json:"tagger"`
	Message string     `json:"message"`
}

// Validate reports whether the tag can be stored and read back: a tag must be
// named. An absent Target is valid (a tag may be created before its target) and
// round-trips as absent. The store enforces it on Put and Get (cas.Validator).
func (g *Tag) Validate() error {
	if g.Name == "" {
		return fmt.Errorf("gitlike: tag has no name")
	}
	return nil
}

// Type returns the versioned type name "tag@1".
func (g *Tag) Type() string { return TypeTag }

// References returns the target digest.
func (g *Tag) References() []cas.Digest {
	if !g.Target.IsZero() {
		return []cas.Digest{g.Target}
	}
	return nil
}

// parseType extracts the unversioned type name ("blob", "tree", ...) from a
// stored object's TLV envelope bytes (see cas.EnvelopeFromBytes). It returns
// ErrUnknownType for a malformed object.
func parseType(data []byte) (string, error) {
	env, err := cas.EnvelopeFromBytes(data)
	if err != nil {
		return "", fmt.Errorf("gitlike: %w", err)
	}
	base, _, _ := strings.Cut(env.Type, "@")
	if base == "" {
		return "", fmt.Errorf("gitlike: %w: object missing type", cas.ErrUnknownType)
	}
	return base, nil
}
