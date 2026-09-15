// Package pack provides an opt-in packfile backend for the cas core.
//
// Packfiles are intentionally not enabled by default: the default loose-object
// filesystem backend remains the lean choice for the common case. This package
// exists as a drop-in extension for stores that need to coalesce many small
// objects into append-only pack files while keeping the core CAS model intact.
package pack
