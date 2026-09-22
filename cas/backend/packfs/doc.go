// Package packfs provides an opt-in append-only packfile backend for a
// content-addressable store.
//
// Packfiles are intentionally not enabled by default: the default loose-object
// filesystem backend remains the lean choice for the common case. This package
// exists as a drop-in extension for stores that need to coalesce many small
// objects into append-only pack files while keeping the content-addressable
// model intact. It is a storage backend, not the helper layer in cas/pack and
// not a codec.
package packfs
