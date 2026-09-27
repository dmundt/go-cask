---
okf_version: "0.2"
title: Design Docs — go-cask
description: Non-normative design documents — orientation, design history, build-engine extraction, viewer briefs and implementation plans. See docs/index.md for top-level rule index.
version: v11
---

# Design Docs — go-cask

Non-normative. Inform, never override instruction specs.

| File | What |
|---|---|
| [`core-overview.md`](core-overview.md) | Orientation: the layer diagram (ASCII and Mermaid) and the component inventory; points at `cas-core.md` §3.3/§4 |
| [`design-history.md`](design-history.md) | How the design converged: the origin conversation, its ten stages, where each landed |
| [`build-engine-extraction.md`](build-engine-extraction.md) | Decision brief: moving the build engine to `build/engine` so another repository can import it — findings, name, staged landings, acceptance |
| [`build-parity-audit.md`](build-parity-audit.md) | What an audit of `scripts/` and `internal/build/` found: the parity gaps, the orphaned work, the process friction, the landings, and the errors — plus the scratch-tree rule it cost |
| [`viewer-brief.md`](viewer-brief.md) | Viewer next-iteration design brief (OpenDesign input) |
| [`object-browser-logic.md`](object-browser-logic.md) | Formal server-state and rendering translation of mockup logic |
| [`viewer-implementation-plan.md`](viewer-implementation-plan.md) | Phased viewer implementation plan |
| [`viewer-template-index.md`](viewer-template-index.md) | Viewer template component inventory and composition tree |
| [`viewer-mockup-parity-audit.md`](viewer-mockup-parity-audit.md) | Viewer mockup comparison and intentional exclusions |
| [`package-graph.md`](package-graph.md) | Generated local package dependency graph (Mermaid), owned by `internal/build/core/depgraph` |
| [`go-cask-viewer.html`](go-cask-viewer.html) | Viewer HTML mockup |
| [`go-cask-object-browser.design.json`](go-cask-object-browser.design.json) | Object browser design artifact |
