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
// Hash references the stored object for Name.
type TreeEntry struct {
	Name string   `json:"name"`
	Hash cas.Hash `json:"hash"`
	Mode string   `json:"mode"`
}

// MarshalJSON implements json.Marshaler: it renders Hash as its "algo:hex"
// string; a nil Hash is omitted from the JSON.
func (e TreeEntry) MarshalJSON() ([]byte, error) {
	type out struct {
		Name string `json:"name"`
		Hash string `json:"hash,omitempty"`
		Mode string `json:"mode"`
	}
	h := ""
	if e.Hash != nil {
		h = e.Hash.String()
	}
	return json.Marshal(out{Name: e.Name, Hash: h, Mode: e.Mode})
}

// UnmarshalJSON implements json.Unmarshaler: it parses the "algo:hex" hash
// string back into Hash. A missing or empty hash leaves Hash nil (a nil
// reference round-trips as nil); a malformed hash returns an error.
func (e *TreeEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Name, e.Mode = raw.Name, raw.Mode
	if raw.Hash != "" {
		h, err := cas.ParseHash(raw.Hash)
		if err != nil {
			return fmt.Errorf("gitlike: decode tree entry hash: %w", err)
		}
		e.Hash = h
	}
	return nil
}

// Tree is a directory-like object referencing other objects by hash.
type Tree struct {
	Entries []TreeEntry `json:"entries"`
}

// Type returns the versioned type name "tree@1".
func (t *Tree) Type() string { return TypeTree }

// References returns the hashes of every entry.
func (t *Tree) References() []cas.Hash {
	refs := make([]cas.Hash, 0, len(t.Entries))
	for _, e := range t.Entries {
		if e.Hash != nil {
			refs = append(refs, e.Hash)
		}
	}
	return refs
}

// Commit points at a tree (and optionally a parent commit); nil Parent marks
// a root commit.
type Commit struct {
	Tree    cas.Hash  `json:"tree"`
	Parent  cas.Hash  `json:"parent,omitempty"`
	Author  string    `json:"author"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// MarshalJSON implements json.Marshaler: it renders the tree and parent
// hashes as "algo:hex" strings; a nil parent is omitted (a commit always
// names a tree).
func (c Commit) MarshalJSON() ([]byte, error) {
	type out struct {
		Tree    string    `json:"tree"`
		Parent  string    `json:"parent,omitempty"`
		Author  string    `json:"author"`
		Message string    `json:"message"`
		Time    time.Time `json:"time"`
	}
	parent := ""
	if c.Parent != nil {
		parent = c.Parent.String()
	}
	return json.Marshal(out{
		Tree:    c.Tree.String(),
		Parent:  parent,
		Author:  c.Author,
		Message: c.Message,
		Time:    c.Time,
	})
}

// UnmarshalJSON implements json.Unmarshaler: it parses the "algo:hex" tree
// and parent strings back into Hash values. The tree hash is required; a
// missing or empty parent leaves Parent nil (a root commit); a malformed hash
// returns an error.
func (c *Commit) UnmarshalJSON(data []byte) error {
	var raw struct {
		Tree    string    `json:"tree"`
		Parent  string    `json:"parent"`
		Author  string    `json:"author"`
		Message string    `json:"message"`
		Time    time.Time `json:"time"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Author, c.Message, c.Time = raw.Author, raw.Message, raw.Time
	tree, err := cas.ParseHash(raw.Tree)
	if err != nil {
		return fmt.Errorf("gitlike: decode commit tree hash: %w", err)
	}
	c.Tree = tree
	if raw.Parent != "" {
		parent, err := cas.ParseHash(raw.Parent)
		if err != nil {
			return fmt.Errorf("gitlike: decode commit parent hash: %w", err)
		}
		c.Parent = parent
	}
	return nil
}

// Type returns the versioned type name "commit@1".
func (c *Commit) Type() string { return TypeCommit }

// References returns the tree hash and the parent hash (if any).
func (c *Commit) References() []cas.Hash {
	refs := make([]cas.Hash, 0, 2)
	if c.Tree != nil {
		refs = append(refs, c.Tree)
	}
	if c.Parent != nil {
		refs = append(refs, c.Parent)
	}
	return refs
}

// Tag names a target object (typically a commit).
type Tag struct {
	Name    string   `json:"name"`
	Target  cas.Hash `json:"target"`
	Tagger  string   `json:"tagger"`
	Message string   `json:"message"`
}

// MarshalJSON implements json.Marshaler: it renders the target hash as its
// "algo:hex" string; a nil target encodes as an empty string.
func (g Tag) MarshalJSON() ([]byte, error) {
	type out struct {
		Name    string `json:"name"`
		Target  string `json:"target"`
		Tagger  string `json:"tagger"`
		Message string `json:"message"`
	}
	target := ""
	if g.Target != nil {
		target = g.Target.String()
	}
	return json.Marshal(out{Name: g.Name, Target: target, Tagger: g.Tagger, Message: g.Message})
}

// UnmarshalJSON implements json.Unmarshaler: it parses the "algo:hex"
// target string back into a Hash value. An empty target leaves Target nil.
func (g *Tag) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name    string `json:"name"`
		Target  string `json:"target"`
		Tagger  string `json:"tagger"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.Name, g.Tagger, g.Message = raw.Name, raw.Tagger, raw.Message
	if raw.Target != "" {
		target, err := cas.ParseHash(raw.Target)
		if err != nil {
			return fmt.Errorf("gitlike: decode tag target hash: %w", err)
		}
		g.Target = target
	}
	return nil
}

// Type returns the versioned type name "tag@1".
func (g *Tag) Type() string { return TypeTag }

// References returns the target hash.
func (g *Tag) References() []cas.Hash {
	if g.Target == nil {
		return nil
	}
	return []cas.Hash{g.Target}
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
