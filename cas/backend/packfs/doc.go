// Package packfs provides an opt-in append-only packfile backend for a
// content-addressable store.
//
// Packfiles are intentionally not enabled by default: the default loose-object
// filesystem backend remains the lean choice for the common case. This package
// exists as a drop-in extension for stores that need to coalesce many small
// objects into append-only pack files while keeping the content-addressable
// model intact. It is a storage backend, not the helper layer in cas/pack and
// not a codec.
//
// Backend implements cas.BatchGetter: cas.GetMany (cas-core §4.13) serves a
// batch of digests with one open per pack file instead of one open per object,
// which is the difference between a load that costs N opens and one that costs
// one. A digest without a usable pack record still comes from the loose
// backend, exactly as Get serves it.
package packfs
