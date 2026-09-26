// Package policy holds go-cask's own build decisions: the layer matrix, the
// coverage tiers, the codec guards, the site's inventory tables, and the package
// graph's prose.
//
// The engine lives in the separate module internal/build/core, which ships no
// tables — a table is one repository's policy. This package is where go-cask's live,
// so a change to what the gate enforces is a change to a file here, and the engine
// that reads it stays reusable by another repository.
package policy

import (
	"github.com/dmundt/go-cask/internal/build/core/coverage"
	"github.com/dmundt/go-cask/internal/build/core/deps"
	"github.com/dmundt/go-cask/internal/build/core/layers"
)

// ModulePath is this repository's module path, which every go-cask-relative prefix
// in these tables is built from.
const ModulePath = "github.com/dmundt/go-cask"

// Matrix is go-cask's layer table, mirroring AGENTS.md "Layers and citizen
// classes". The order matters only for reporting: the first layer that claims a
// package owns it.
//
// Keep it in step with that section: an arm here the prose does not state, or a
// prose row missing here, is the drift this table exists to prevent.
func Matrix() []layers.Layer {
	local := func(suffix string) string { return ModulePath + suffix }
	return []layers.Layer{
		{
			// The generic core. It may import only itself.
			Name:    "cas/",
			Owns:    layers.OwnsTree(local("/cas")),
			Allowed: []string{local("/cas")},
		},
		{
			// The reference object model at the application layer: it may use the
			// core and nothing else in this module.
			Name:    "gitlike/",
			Owns:    layers.OwnsExact(local("/gitlike")),
			Allowed: []string{local("/cas")},
		},
		{
			// The product. It may use the core and its own internal packages, but
			// never gitlike (the reference library is not a dependency of the
			// product) and never examples.
			Name:    "internal/, cmd/",
			Owns:    layers.OwnsAnyTree(local("/internal"), local("/cmd")),
			Allowed: []string{local("/cas"), local("/internal")},
		},
		{
			// Teaching code: it may copy the reference model, so it may import
			// gitlike as well as the core, and never internal/ or cmd/.
			Name:    "examples/, benchmarks/",
			Owns:    layers.OwnsAnyTree(local("/examples"), local("/benchmarks")),
			Allowed: []string{local("/cas"), local("/gitlike")},
		},
	}
}

// Coverage is go-cask's coverage policy, as scripts/verify.sh once declared it in
// bash. It is a function rather than a package-level variable so a caller cannot
// mutate the gate's policy by accident.
//
// docs/specs/testing-strategy.md §5 states the rule and omits the list, so a
// threshold change touches one file. The tier thresholds are the gate's contract
// with that section: editing a number here changes what the gate accepts, so it is
// a specification change, not a refactor.
func Coverage() coverage.Policy {
	return coverage.Policy{
		Targets: []coverage.Target{
			// Tier 90.
			{Threshold: 90, Package: "cas", Tier: "core"},
			{Threshold: 90, Package: "cas/backend", Tier: "backend"},
			{Threshold: 90, Package: "cas/backend/fs", Tier: "backend"},
			{Threshold: 90, Package: "cas/backend/mem", Tier: "backend"},
			{Threshold: 90, Package: "cas/backend/snapshot", Tier: "backend"},
			{Threshold: 90, Package: "cas/repo", Tier: "reference"},
			{Threshold: 90, Package: "cas/refs", Tier: "reference"},
			{Threshold: 90, Package: "cas/pack", Tier: "reference"},
			{Threshold: 90, Package: "cas/codec/flate", Tier: "codec"},
			{Threshold: 90, Package: "cas/codec/internal/bounded", Tier: "codec"},
			{Threshold: 90, Package: "cas/bloom/persistent", Tier: "index"},
			{Threshold: 90, Package: "internal/store", Tier: "seam"},
			// Tier 80.
			{Threshold: 80, Package: "cas/backend/packfs", Tier: "backend"},
			{Threshold: 80, Package: "cas/bloom", Tier: "index"},
			{Threshold: 80, Package: "cas/bloom/counting", Tier: "index"},
			{Threshold: 80, Package: "cas/bloom/standard", Tier: "index"},
			{Threshold: 80, Package: "cas/cache", Tier: "support"},
			{Threshold: 80, Package: "cas/cache/mem", Tier: "support"},
			{Threshold: 80, Package: "cas/cache/lru", Tier: "support"},
			{Threshold: 80, Package: "cas/cache/prefetch", Tier: "support"},
			{Threshold: 80, Package: "cas/codec/binary", Tier: "codec"},
			{Threshold: 80, Package: "cas/codec/cbor", Tier: "codec"},
			{Threshold: 80, Package: "cas/codec/gob", Tier: "codec"},
			{Threshold: 80, Package: "cas/codec/gzip", Tier: "codec"},
			{Threshold: 80, Package: "cas/codec/json", Tier: "codec"},
			{Threshold: 80, Package: "cas/codec/zlib", Tier: "codec"},
			{Threshold: 80, Package: "cas/hash", Tier: "hash"},
			{Threshold: 80, Package: "cas/hash/sha256", Tier: "hash"},
			{Threshold: 80, Package: "cas/hash/sha512", Tier: "hash"},
			{Threshold: 80, Package: "cas/hash/sha512_256", Tier: "hash"},
			{Threshold: 80, Package: "cas/verify/adler32", Tier: "verify"},
			{Threshold: 80, Package: "cas/verify/crc32", Tier: "verify"},
			{Threshold: 80, Package: "cas/verify/crc64", Tier: "verify"},
			{Threshold: 80, Package: "cas/verify/sidecar", Tier: "verify"},
			{Threshold: 80, Package: "gitlike", Tier: "reference"},
			{Threshold: 80, Package: "internal/index", Tier: "support"},
			{Threshold: 80, Package: "internal/web", Tier: "viewer"},
			{Threshold: 80, Package: "cmd/cask", Tier: "command"},
		},
		// Deliberately ungated packages, as a package and a written reason. Empty:
		// every package under cas/ carries a numeric gate.
		Exempt: []coverage.Exemption{},
	}
}

// CodecGuards are the packages that must stay independent of the codec layer.
//
//   - `gitlike` names no wire format: `NewRepository` takes the caller's
//     `gitlike.Codecs` set (cas-core §4.12), so importing a codec would make the
//     reference model choose for the caller.
//   - `cas/pack` is the helper layer that must stay codec-agnostic like the
//     reference layer: the caller passes the codec to
//     `EncodeWith`/`DecodeWith`/`LoadWith`/`SaveWith`, and the package reports
//     `ErrNilCodec` instead of choosing a format (go-cask#307).
func CodecGuards() []deps.CodecGuard {
	return []deps.CodecGuard{
		{Package: "./gitlike", Remedy: "inject codecs via gitlike.Codecs"},
		{Package: "./cas/pack", Remedy: "the caller passes the codec"},
	}
}
