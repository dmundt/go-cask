// Package test provides shared test types and helpers for the cas module's
// test suites. Internal use only.
package test

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Note is a test Object[T] used across the test suite.
type Note struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

func (Note) Type() string           { return "note@1" }
func (Note) References() []cas.Hash { return nil }

// Node is a test Object[T] with references, used to test Walker/References.
type Node struct {
	Name string     `json:"name"`
	Refs []cas.Hash `json:"refs,omitempty"`
}

func (Node) Type() string             { return "node@1" }
func (n Node) References() []cas.Hash { return n.Refs }

// MarshalJSON encodes Node with Refs as hex strings for JSON round-tripping.
func (n Node) MarshalJSON() ([]byte, error) {
	s := struct {
		Name string   `json:"name"`
		Refs []string `json:"refs,omitempty"`
	}{Name: n.Name}
	for _, r := range n.Refs {
		s.Refs = append(s.Refs, r.String())
	}
	return json.Marshal(s)
}

// UnmarshalJSON decodes Node from the JSON format produced by MarshalJSON.
func (n *Node) UnmarshalJSON(data []byte) error {
	var s struct {
		Name string   `json:"name"`
		Refs []string `json:"refs,omitempty"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	n.Name = s.Name
	for _, r := range s.Refs {
		h, err := cas.ParseHash(r)
		if err != nil {
			return err
		}
		n.Refs = append(n.Refs, h)
	}
	return nil
}

// ErrorObj is a test Object[T] used in error-path tests.
type ErrorObj struct{}

func (ErrorObj) Type() string           { return "err@1" }
func (ErrorObj) References() []cas.Hash { return nil }

// FailingCodec[T] is a Codec[T] that always fails on Encode.
type FailingCodec[T any] struct{}

func (FailingCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode exploded") }
func (FailingCodec[T]) Decode([]byte) (T, error) { var z T; return z, nil }

// HashData is a test helper for hashing data.
func HashData(algo string, data []byte) (cas.Hash, error) {
	return cas.HashBytes(algo, data)
}

// ReadAllAndClose reads all bytes from rc and closes it.
func ReadAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}
