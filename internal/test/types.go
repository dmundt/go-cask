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
	// Title is the test note title.
	Title string `json:"title"`
	// Body is the test note body.
	Body string `json:"body,omitempty"`
}

// Type returns the test note type name.
func (Note) Type() string { return "note@1" }

// References returns nil because test notes are leaves.
func (Note) References() []cas.Digest { return nil }

// Node is a test Object[T] with references, used to test Walker/References.
type Node struct {
	// Name is the node name.
	Name string `json:"name"`
	// Refs lists referenced node digests.
	Refs []cas.Digest `json:"refs,omitempty"`
}

// Type returns the test node type name.
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

// Type returns the error-object type name.
func (ErrorObj) Type() string { return "err@1" }

// References returns nil because error objects are leaves.
func (ErrorObj) References() []cas.Digest { return nil }

// FailingCodec[T] is a Codec[T] that always fails on Encode.
type FailingCodec[T any] struct{}

// Encode always returns an error.
func (FailingCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode exploded") }

// Decode returns the zero value without error.
func (FailingCodec[T]) Decode([]byte) (T, error) { var z T; return z, nil }

// DigestData is a test helper for digesting data with the shipped sha256 hasher.
func DigestData(data []byte) cas.Digest {
	return sha256.Of(data)
}

// ReadAllAndClose reads all bytes from rc and closes it.
func ReadAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}
