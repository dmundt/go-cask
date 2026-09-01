// Package gitlike implements the reference object model for the cas core: a
// Git-like Blob/Tree/Commit/Tag model layered on the generic Store[T]
// primitives (cas-core §4.12). It is an example — applications define their
// own Object[T] types and their own repository/resolver combinations; the
// gitlike package demonstrates the pattern and is NOT part of the cas core.
//
// Every object type is versioned from the start (blob@1, tree@1, commit@1,
// tag@1) and stored in the self-describing envelope
// {"type": "<type>@<major>", "data": "<base64 payload>"} defined by the core.
//
// The package also provides Repository (per-type stores over one RawStore),
// Resolver/ResolvedObject (cross-type resolution without any), WalkGraph
// (whole-graph traversal), CachedRepository (per-type LRU caches) and
// Preloader (background commit preloading).
package gitlike

import (
	"encoding/base64"
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

// Serialize returns the self-describing envelope for the blob.
func (b *Blob) Serialize() ([]byte, error) {
	// json.Marshal cannot fail here: Blob holds only a []byte field.
	payload, _ := json.Marshal(b)
	return marshalEnvelope(b.Type(), payload)
}

// Deserialize parses the envelope and returns the blob.
func (b *Blob) Deserialize(data []byte) (*Blob, error) {
	_, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, err
	}
	var v Blob
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("gitlike: decode blob: %w", err)
	}
	return &v, nil
}

// TreeEntry is one entry in a Tree. It is an entry, not an object itself;
// Hash references the stored object for Name.
type TreeEntry struct {
	Name string   `json:"name"`
	Hash cas.Hash `json:"hash"`
	Mode string   `json:"mode"`
}

// MarshalJSON renders Hash as its "algo:hex" string (nil → omitted).
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

// UnmarshalJSON parses the "algo:hex" string form back into a Hash.
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

// Serialize returns the self-describing envelope for the tree.
func (t *Tree) Serialize() ([]byte, error) {
	// json.Marshal cannot fail here: Tree serializes via plain struct fields.
	payload, _ := json.Marshal(t)
	return marshalEnvelope(t.Type(), payload)
}

// Deserialize parses the envelope and returns the tree.
func (t *Tree) Deserialize(data []byte) (*Tree, error) {
	_, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, err
	}
	var v Tree
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("gitlike: decode tree: %w", err)
	}
	return &v, nil
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

// MarshalJSON renders the hashes as "algo:hex" strings.
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

// UnmarshalJSON parses the "algo:hex" string hashes back into Hash values.
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

// Serialize returns the self-describing envelope for the commit.
func (c *Commit) Serialize() ([]byte, error) {
	// json.Marshal cannot fail here: Commit serializes via plain struct fields.
	payload, _ := json.Marshal(c)
	return marshalEnvelope(c.Type(), payload)
}

// Deserialize parses the envelope and returns the commit.
func (c *Commit) Deserialize(data []byte) (*Commit, error) {
	_, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, err
	}
	var v Commit
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("gitlike: decode commit: %w", err)
	}
	return &v, nil
}

// Tag names a target object (typically a commit).
type Tag struct {
	Name    string   `json:"name"`
	Target  cas.Hash `json:"target"`
	Tagger  string   `json:"tagger"`
	Message string   `json:"message"`
}

// MarshalJSON renders the target hash as an "algo:hex" string.
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

// UnmarshalJSON parses the "algo:hex" target hash back into a Hash value.
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

// Serialize returns the self-describing envelope for the tag.
func (g *Tag) Serialize() ([]byte, error) {
	// json.Marshal cannot fail here: Tag serializes via plain struct fields.
	payload, _ := json.Marshal(g)
	return marshalEnvelope(g.Type(), payload)
}

// Deserialize parses the envelope and returns the tag.
func (g *Tag) Deserialize(data []byte) (*Tag, error) {
	_, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, err
	}
	var v Tag
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("gitlike: decode tag: %w", err)
	}
	return &v, nil
}

// envelope is the self-describing storage form (cas-core §8 decision 1):
// {"type": "<type>@<major>", "data": "<base64 payload>"}.
type envelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// marshalEnvelope wraps a serialized payload in the self-describing envelope.
func marshalEnvelope(typeName string, payload []byte) ([]byte, error) {
	return json.Marshal(envelope{Type: typeName, Data: base64.StdEncoding.EncodeToString(payload)})
}

// unmarshalEnvelope parses the envelope and returns the versioned type name
// and the base64-decoded payload.
func unmarshalEnvelope(data []byte) (string, []byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", nil, fmt.Errorf("gitlike: %w: not a valid object envelope", cas.ErrUnknownType)
	}
	if env.Type == "" {
		return "", nil, fmt.Errorf("gitlike: %w: envelope missing type", cas.ErrUnknownType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", nil, fmt.Errorf("gitlike: %w: envelope data is not base64", cas.ErrUnknownType)
	}
	return env.Type, payload, nil
}

// parseType extracts the unversioned type name ("blob", "tree", ...) from a
// stored object's envelope bytes; an absent major version reads as the
// legacy default (object-versioning §2). It returns ErrUnknownType for a
// malformed envelope.
func parseType(data []byte) (string, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("gitlike: %w: not a valid object envelope", cas.ErrUnknownType)
	}
	base, _, _ := strings.Cut(env.Type, "@")
	if base == "" {
		return "", fmt.Errorf("gitlike: %w: envelope missing type", cas.ErrUnknownType)
	}
	return base, nil
}
