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
	"encoding/base64"
	"encoding/json"
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

// Note references tags, attachments, and related notes by hash.
type Note struct {
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Tags        []cas.Hash `json:"tags,omitempty"`
	Attachments []cas.Hash `json:"attachments,omitempty"`
	Related     []cas.Hash `json:"related,omitempty"`
}

func (n *Note) Type() string { return typeNote }

func (n *Note) References() []cas.Hash {
	refs := make([]cas.Hash, 0, len(n.Tags)+len(n.Attachments)+len(n.Related))
	refs = append(refs, n.Tags...)
	refs = append(refs, n.Attachments...)
	refs = append(refs, n.Related...)
	return refs
}

// MarshalJSON renders hash slices as "algo:hex" strings.
func (n Note) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title       string   `json:"title"`
		Body        string   `json:"body"`
		Tags        []string `json:"tags,omitempty"`
		Attachments []string `json:"attachments,omitempty"`
		Related     []string `json:"related,omitempty"`
	}{n.Title, n.Body, hashStrings(n.Tags), hashStrings(n.Attachments), hashStrings(n.Related)})
}

// UnmarshalJSON parses the string hashes back into Hash values.
func (n *Note) UnmarshalJSON(data []byte) error {
	var raw struct {
		Title       string   `json:"title"`
		Body        string   `json:"body"`
		Tags        []string `json:"tags"`
		Attachments []string `json:"attachments"`
		Related     []string `json:"related"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	n.Title, n.Body = raw.Title, raw.Body
	var err error
	if n.Tags, err = parseHashes(raw.Tags); err != nil {
		return err
	}
	if n.Attachments, err = parseHashes(raw.Attachments); err != nil {
		return err
	}
	if n.Related, err = parseHashes(raw.Related); err != nil {
		return err
	}
	return nil
}

func (n *Note) Serialize() ([]byte, error) {
	return marshalEnvelope(n.Type(), n)
}

func (n *Note) Deserialize(data []byte) (*Note, error) {
	payload, err := envelopePayload(data)
	if err != nil {
		return nil, err
	}
	var v Note
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Tag is a leaf object a note references.
type Tag struct {
	Name string `json:"name"`
}

func (t *Tag) Type() string           { return typeTag }
func (t *Tag) References() []cas.Hash { return nil }
func (t *Tag) Serialize() ([]byte, error) {
	return marshalEnvelope(t.Type(), t)
}
func (t *Tag) Deserialize(data []byte) (*Tag, error) {
	payload, err := envelopePayload(data)
	if err != nil {
		return nil, err
	}
	var v Tag
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Attachment is a large blob, loaded lazily on access.
type Attachment struct {
	Data []byte `json:"data"`
}

func (a *Attachment) Type() string           { return typeAttachment }
func (a *Attachment) References() []cas.Hash { return nil }
func (a *Attachment) Serialize() ([]byte, error) {
	return marshalEnvelope(a.Type(), a)
}
func (a *Attachment) Deserialize(data []byte) (*Attachment, error) {
	payload, err := envelopePayload(data)
	if err != nil {
		return nil, err
	}
	var v Attachment
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func hashStrings(hashes []cas.Hash) []string {
	out := make([]string, 0, len(hashes))
	for _, h := range hashes {
		out = append(out, h.String())
	}
	return out
}

func parseHashes(strs []string) ([]cas.Hash, error) {
	out := make([]cas.Hash, 0, len(strs))
	for _, s := range strs {
		h, err := cas.ParseHash(s)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// envelope is the self-describing storage form (cas-core §8 decision 1).
type envelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func marshalEnvelope(typeName string, v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Type: typeName, Data: base64.StdEncoding.EncodeToString(payload)})
}

func envelopePayload(data []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: not a valid object envelope", cas.ErrUnknownType)
	}
	if env.Type == "" {
		return nil, fmt.Errorf("%w: envelope missing type", cas.ErrUnknownType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope data is not base64", cas.ErrUnknownType)
	}
	return payload, nil
}

// parseType extracts the unversioned type name ("note", "tag", ...).
func parseType(data []byte) (string, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("%w: not a valid object envelope", cas.ErrUnknownType)
	}
	base, _, _ := strings.Cut(env.Type, "@")
	if base == "" {
		return "", fmt.Errorf("%w: envelope missing type", cas.ErrUnknownType)
	}
	return base, nil
}
