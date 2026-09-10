// Package test provides shared test types and helpers for the cas module's
// test suites. Internal use only.
package test

import (
	"errors"
	"io"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// Note is a test Object[T] used across the test suite.
type Note struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

func (Note) Type() string             { return "note@1" }
func (Note) References() []cas.Digest { return nil }

// Node is a test Object[T] with references, used to test Walker/References.
type Node struct {
	Name string       `json:"name"`
	Refs []cas.Digest `json:"refs,omitempty"`
}

func (Node) Type() string { return "node@1" }

// References returns the non-absent references, or nil for a leaf node (nil
// means "no references", matching Blob.References). A cas.Digest renders itself
// as one hex string through encoding.TextMarshaler, so the object needs no JSON
// code for references (cas-core §4.2).
func (n Node) References() []cas.Digest {
	if len(n.Refs) == 0 {
		return nil
	}
	refs := make([]cas.Digest, 0, len(n.Refs))
	for _, d := range n.Refs {
		if !d.IsZero() {
			refs = append(refs, d)
		}
	}
	return refs
}

// ErrorObj is a test Object[T] used in error-path tests.
type ErrorObj struct{}

func (ErrorObj) Type() string             { return "err@1" }
func (ErrorObj) References() []cas.Digest { return nil }

// FailingCodec[T] is a Codec[T] that always fails on Encode.
type FailingCodec[T any] struct{}

func (FailingCodec[T]) Marshal(T) ([]byte, error)   { return nil, errors.New("marshal exploded") }
func (FailingCodec[T]) Unmarshal([]byte) (T, error) { var z T; return z, nil }

// HashData is a test helper for digesting data with the shipped sha256 hasher.
func HashData(data []byte) cas.Digest {
	return sha256.Of(data)
}

// ReadAllAndClose reads all bytes from rc and closes it.
func ReadAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}
