# Specifications overview

The repository keeps its normative rules under `docs/specs/`. Those files
define the architecture and implementation contracts the project depends on.
This page links to the ones most relevant to someone using go-cask as a
library, not to the internal maintenance/process files.

## Start here

- [cas-core](https://github.com/dmundt/go-cask/blob/main/docs/specs/cas-core.md) —
  the canonical core library specification: every component contract, data
  flows, and concurrency guarantees
- [object-versioning](https://github.com/dmundt/go-cask/blob/main/docs/specs/object-versioning.md) —
  versioned type names and model compatibility rules
- [library-design](https://github.com/dmundt/go-cask/blob/main/docs/specs/library-design.md) —
  the exported-surface budget and compatibility policy
- [versioning](https://github.com/dmundt/go-cask/blob/main/docs/specs/versioning.md) —
  semantic versioning and release process
- [consistency](https://github.com/dmundt/go-cask/blob/main/docs/specs/consistency.md) —
  the mark-and-sweep GC and pruning model
- [operations](https://github.com/dmundt/go-cask/blob/main/docs/specs/operations.md) —
  durability, crash recovery, and integrity cadence
- [testing-strategy](https://github.com/dmundt/go-cask/blob/main/docs/specs/testing-strategy.md) —
  the test classes that prove the CAS invariants

## What you will find

- object format and identity rules
- storage and backend contract expectations
- compatibility and migration guarantees
- versioning and release design
- viewer and security requirements

## Why the website also includes a summary

The repository specs are authoritative. The website makes the project easier
to navigate for new users: read the short architecture summary here, then
follow a link directly to the specification you need.
