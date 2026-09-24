// Package design holds the repository's design-rule checks: rules the specs
// state, expressed as ordinary Go tests so that `go test ./...` in the gate
// enforces them instead of relying on a reviewer's memory.
//
// The first inhabitant is library-design §5, "no `any`/`interface{}` in the
// exported API". As the spec defines it, the rule bans `any` as an exported
// value, parameter or result type; an unconstrained type parameter
// (`Codec[T any]`, `Object[T any]`) is Go's constraint syntax rather than a
// value type, so it is allowed; and `cas/codec/cbor`'s dynamic value codec is
// the one recorded exception (go-cask#191). A new exported `any` therefore fails
// the gate until the symbol is named in the check's allow-list, which is exactly
// the "explicit ratification" the rule asks for — and a stale entry fails too,
// so an exemption cannot rot into blanket permission.
package design
