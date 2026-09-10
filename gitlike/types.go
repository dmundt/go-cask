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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
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
func (b *Blob) References() []cas.Hash { return nil }

// TreeEntry is one entry in a Tree. It is an entry, not an object itself;
// Hash references the stored object for Name and may be absent.
type TreeEntry struct {
	Name string         `json:"name"`
	Hash jsoncodec.Hash `json:"hash,omitzero"` // optional: absent is omitted
	Mode string         `json:"mode"`
}

// Validate reports whether the entry can be stored and read back: a tree entry
// must be named. An absent Hash is valid — an entry without a reference is
// representable and round-trips as absent. Validation is advisory;
// Tree.Validate runs it over a whole tree.
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

// Validate reports whether every entry can be stored and read back. It is
// advisory: Store.Put marshals an object, it does not validate one, so callers
// building a tree by hand SHOULD call Validate before Put.
func (t *Tree) Validate() error {
	for i, e := range t.Entries {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
	}
	return nil
}

// References returns the hashes of every entry.
func (t *Tree) References() []cas.Hash {
	refs := make([]cas.Hash, 0, len(t.Entries))
	for _, e := range t.Entries {
		if h := e.Hash.Hash(); !h.IsZero() {
			refs = append(refs, h)
		}
	}
	return refs
}

// Commit points at a tree (and optionally a parent commit); an absent Parent
// marks a root commit.
type Commit struct {
	Tree    jsoncodec.Hash `json:"tree"`            // required: a missing/empty/null tree fails decode
	Parent  jsoncodec.Hash `json:"parent,omitzero"` // optional: absent is omitted
	Author  string         `json:"author"`
	Message string         `json:"message"`
	Time    time.Time      `json:"time"`
}

// Validate reports whether the commit can be stored and read back: a commit
// must name a tree. Store.Put enforces this through MarshalJSON, so an
// unreadable commit cannot be written; calling Validate directly lets a caller
// check a hand-built object before Put.
func (c *Commit) Validate() error {
	if c.Tree.IsZero() {
		return fmt.Errorf("gitlike: commit has no tree")
	}
	return nil
}

// MarshalJSON implements json.Marshaler. It only enforces the mandatory tree;
// the hash fields render themselves through jsoncodec.Hash, the JSON codec's
// field type (cas-core §4.2), so no hand-written hash rendering is involved.
func (c Commit) MarshalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	type plain Commit // no methods: marshals by field, no recursion
	return json.Marshal(plain(c))
}

// UnmarshalJSON implements json.Unmarshaler. The hash fields decode themselves
// (jsoncodec.Hash validates every reference), so this method only exists to keep
// the required tree strict: a missing, empty, or null tree is a decode error
// rather than a silently rootless commit. An absent parent decodes as absent.
func (c *Commit) UnmarshalJSON(data []byte) error {
	type plain Commit // no methods: decodes by field, no recursion
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.Tree.IsZero() {
		return fmt.Errorf("gitlike: commit has no tree")
	}
	*c = Commit(p)
	return nil
}

// Type returns the versioned type name "commit@1".
func (c *Commit) Type() string { return TypeCommit }

// References returns the tree hash and the parent hash (if any).
func (c *Commit) References() []cas.Hash {
	refs := make([]cas.Hash, 0, 2)
	if h := c.Tree.Hash(); !h.IsZero() {
		refs = append(refs, h)
	}
	if h := c.Parent.Hash(); !h.IsZero() {
		refs = append(refs, h)
	}
	return refs
}

// Tag names a target object (typically a commit).
type Tag struct {
	Name    string         `json:"name"`
	Target  jsoncodec.Hash `json:"target"` // may be absent; a plain field keeps the historical ""
	Tagger  string         `json:"tagger"`
	Message string         `json:"message"`
}

// Validate reports whether the tag can be stored and read back: a tag must be
// named. An absent Target is valid (a tag may be created before its target) and
// round-trips as absent. Validation is advisory — Store.Put marshals, it does
// not validate.
func (g *Tag) Validate() error {
	if g.Name == "" {
		return fmt.Errorf("gitlike: tag has no name")
	}
	return nil
}

// Type returns the versioned type name "tag@1".
func (g *Tag) Type() string { return TypeTag }

// References returns the target hash.
func (g *Tag) References() []cas.Hash {
	if h := g.Target.Hash(); !h.IsZero() {
		return []cas.Hash{h}
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
