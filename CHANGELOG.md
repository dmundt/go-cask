# Changelog

Notable user-facing changes only. Routine maintenance, test-only changes,
internal refactors, and release preparation are omitted.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Opening the viewer on an empty store no longer fails while preview graph
  metadata is unavailable.

## [v1.6.3] - 2026-09-22

### Changed

- Viewer object-list rendering now scales with visible rows for the default
  hash-ordered view, avoiding per-object formatting and sorting work for
  large stores.
- Added opt-in viewer scale benchmarks for 100 through 100,000 stored objects.

## [v1.6.2] - 2026-09-22

### Changed

- Refined the embedded viewer into a denser VS Code-style workbench with flat
  docked panels, compact type and table rhythm, quieter inspector/status
  presentation, and consistent interactive states.

## [v1.6.1] - 2026-09-22

### Changed

- Viewer object browsing reuses bounded metadata snapshots, reducing repeated
  filesystem scans during filtering, sorting, pagination, and htmx refreshes.
- Viewer verification reports the mismatched digest from its existing hash pass
  instead of reading corrupt objects a second time.

## [v1.6.0] - 2026-09-22

### Added

- `cask web` accepts `-hash-algo sha256|sha512|sha512_256`; the selected
  algorithm is shown in object metadata.

### Changed

- Viewer digest parsing and verification use the configured `cas.Hasher`
  instead of assuming SHA-256.
- Viewer object routes use `/dump` for the HTML byte dump and `/verify` for
  bulk verification.
- Added a subtle gray separator between the object table and inspector,
  matching the viewer's existing border system.

## [v1.5.0] - 2026-09-21

### Added

- Secure viewer response headers and a restrictive content security policy.
- Viewer-wide object verification with per-object results and audit records.
- Automatic object selection, reference navigation, and inspector history.
- Build version in the viewer top bar.

### Changed

- Viewer object browsing, filtering, sorting, reachability, references, and
  inspection were consolidated into one object-browser workspace.
- Viewer metadata reads are cached for immutable objects.
- Viewer controls and inspector layout were tightened for consistent sizing
  and keyboard-accessible navigation.

### Removed

- Viewer dashboard, garbage-collection page, delete action, and custom
  JavaScript. Destructive maintenance remains a CLI operation.

## [v1.4.6] - 2026-09-18

### Changed

- Refined the published documentation site navigation, search, and responsive
  layout.

## [v1.4.5] - 2026-09-17

### Fixed

- Hardened verification and release automation.
- Improved pack storage recovery and documentation.

## [v1.4.4] - 2026-09-16

### Added

- Optional packfile storage for large object stores.
- Persistent Bloom-filter improvements and cross-platform mapped-file support.

## [v1.4.3] - 2026-09-15

### Changed

- Consolidated codec APIs and refreshed performance-sensitive paths.

## [v1.4.2] - 2026-09-15

### Added

- Gzip codec and expanded codec documentation.

## [v1.4.1] - 2026-09-15

### Added

- Packfile backend and additional fuzz coverage.

## [v1.4.0] - 2026-09-15

### Added

- Advisory Bloom layer for faster object-set membership checks.
- Focused codec and hasher benchmarks.

## [v1.3.0] - 2026-09-10

### Added

- Binary codec and digest-prefix support.
- Consumer-facing examples covering the core and Git-like APIs.

### Changed

- Core storage is hash-algorithm agnostic through client-injected hashers.
- Git-like repositories receive codecs explicitly and enforce object
  invariants independently of serialization.

## [v1.2.0] - 2026-09-10

### Added

- Typed object stores, codecs, caching, graph traversal, and filesystem
  storage capabilities.

## [v1.1.0] - 2026-09-09

### Added

- Initial Git-like object model and repository APIs on top of the generic CAS
  core.

## [v1.0.0] - 2026-09-09

### Added

- First stable release of the generic, content-addressable storage core,
  filesystem backend, typed object layer, and Git-like reference model.

## [v0.3.0] - 2026-09-09

- Stabilized the core object, codec, backend, and repository APIs ahead of
  the 1.0 release.

## [v0.2.0] - 2026-09-09

- Added the typed object and codec layers above the byte backend.

## [v0.1.1] - 2026-09-09

- Improved initial storage performance and examples.

## [v0.1.0] - 2026-09-08

- Initial filesystem-backed content-addressable store and typed API.

## [v0.1.0-alpha.2] - 2026-09-03

- Added the first Git-like object and repository examples.

## [v0.1.0-alpha.1] - 2026-09-03

- Initial public design and prototype APIs.

[Unreleased]: https://github.com/dmundt/go-cask/compare/v1.6.2...HEAD
[v1.6.2]: https://github.com/dmundt/go-cask/compare/v1.6.1...v1.6.2
[v1.6.1]: https://github.com/dmundt/go-cask/compare/v1.6.0...v1.6.1
[v1.6.0]: https://github.com/dmundt/go-cask/compare/v1.5.0...v1.6.0
[v1.5.0]: https://github.com/dmundt/go-cask/compare/v1.4.6...v1.5.0
[v1.4.6]: https://github.com/dmundt/go-cask/compare/v1.4.5...v1.4.6
[v1.4.5]: https://github.com/dmundt/go-cask/compare/v1.4.4...v1.4.5
[v1.4.4]: https://github.com/dmundt/go-cask/compare/v1.4.3...v1.4.4
[v1.4.3]: https://github.com/dmundt/go-cask/compare/v1.4.2...v1.4.3
[v1.4.2]: https://github.com/dmundt/go-cask/compare/v1.4.1...v1.4.2
[v1.4.1]: https://github.com/dmundt/go-cask/compare/v1.4.0...v1.4.1
[v1.4.0]: https://github.com/dmundt/go-cask/compare/v1.3.1...v1.4.0
[v1.3.0]: https://github.com/dmundt/go-cask/compare/v1.2.0...v1.3.0
[v1.2.0]: https://github.com/dmundt/go-cask/compare/v1.1.0...v1.2.0
[v1.1.0]: https://github.com/dmundt/go-cask/compare/v1.0.2...v1.1.0
[v1.0.0]: https://github.com/dmundt/go-cask/compare/v0.3.0...v1.0.0
[v0.3.0]: https://github.com/dmundt/go-cask/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/dmundt/go-cask/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/dmundt/go-cask/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/dmundt/go-cask/releases/tag/v0.1.0
[v0.1.0-alpha.2]: https://github.com/dmundt/go-cask/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[v0.1.0-alpha.1]: https://github.com/dmundt/go-cask/releases/tag/v0.1.0-alpha.1
