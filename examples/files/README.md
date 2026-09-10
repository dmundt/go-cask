# files — a miniature Git on CASK

**What it demonstrates.** A runnable CLI storing file trees as content-addressable objects and committing them, using the `gitlike` layer end to end (examples spec §3.1). Acceptance: `add → commit → log → cat` round-trips; identical content deduplicates across commits; `verify` detects on-disk corruption; `audit` reports each object's derived state (verified/orphaned/corrupt/unverified) from `HEAD` + `Verify`.

## `cas` core parts used

| Component | Where |
|---|---|
| `fs.Backend` (default fan-out 2/1) | `newApp` — the on-disk backend |
| `gitlike.Repository` (per-type `Store[T]` over one `cas.Backend`) | `app.repo` |
| `gitlike.Codecs` (the JSON codec per type: `json.New[gitlike.Blob]()` …) | `newApp` |
| `gitlike.Blob`/`Tree`/`Commit` — `Object[T]`; `Commit.Validate` (a commit must name a tree) | `add`, `commit` |
| `Repository.Blobs/Trees/Commits.Put`, `Get` | store/read objects |
| `Resolver.ResolveAny` / `WalkGraph` | `cat`, `graph`, `audit` reachability |
| `fs.Backend.Verify` / `List` / `Stats` (`cas.Stats`) | `verify`, `audit`, `stats` |
| `cas.Digest` / `sha256.Parse` / `sha256.Format` | ref files (`HEAD`, `INDEX`) and digest args |
| `sha256.New()` (the client's `cas.Hasher`) | the `gitlike.Repository` and every `Verify` call |
| `cas.Validator` (`Commit.Validate`, enforced by `Store.Put`/`Get`) | writing and reading commits |

## What it extends

Nothing — a pure consumer; `cas` and `gitlike` are untouched. Only app additions: the CLI and two ref files (`HEAD` = current commit, `INDEX` = current tree) at the store root, which the store's `List`/`Stats` ignore. The ref files hold the printable `sha256:hexdigest` form (`sha256.Format`; `sha256.Parse` accepts bare hex too).

## Code walkthrough

- `main.go` — the `app` struct (`newApp` wires `fs.Backend` + `gitlike.Repository` over `sha256.New()` and a `gitlike.Codecs` set built from `json.New[*gitlike.…]()` per type; `readRef`/`writeRef` persist digests; `currentTree`/`headCommit` read `INDEX`/`HEAD`) and the argument-parsing CLI:
  - `add <file...>` — `repo.Blobs.Put` (dedup by content digest), builds a `gitlike.Tree`, `Trees.Put`, writes `INDEX`;
  - `commit -m <msg>` — reads `INDEX`, creates a `Commit` (parent = old `HEAD`), `Commits.Put`, advances `HEAD`;
  - `log` — walks the `Commit.Parent` chain; `cat <hash>` — `ResolveAny` → `Blob.Data`; `graph` — `WalkGraph` from `HEAD`;
  - `audit [-no-verify]` — classifies every stored object (below); `verify`/`stats` — `fs.Backend.Verify` per object / `Stats`.
- `audit.go` — the derived-state report: `List` → mark the reachable set from `HEAD` via `References()` (`markReachable`) → `Verify` each → assign a state:

  | State | Meaning |
  |---|---|
  | `verified` | intact and reachable from `HEAD` |
  | `orphaned` | intact but unreachable — a GC candidate (consistency §4) |
  | `corrupt` | `Verify` failed — reported even if orphaned |
  | `unverified` | reachable but not checked (`-no-verify`) |

  States are **derived, never stored** — the point-in-time result of `Verify` + reachability, not store metadata (consistency §8). `-no-verify` skips integrity for a fast orphan scan.

```mermaid
flowchart TB
    A["add file..."] --> B["Blobs.Put (dedup)"] --> C["Tree.Put"] --> I["write INDEX"]
    I --> D["commit -m"] --> E["Commit.Put (parent = HEAD)"] --> H["write HEAD"]
    H --> F["log / cat / graph"]
    H --> G["verify / stats"]
    H --> J["audit: List → mark reachable → Verify each → state"]
```

## How to run

```text
go run ./examples/files -store ./objects add a.txt b.txt
go run ./examples/files -store ./objects commit -m "initial"
go run ./examples/files -store ./objects log
go run ./examples/files -store ./objects stats
go run ./examples/files -store ./objects audit
go run ./examples/files -store ./objects audit -no-verify
go run ./examples/files -store ./objects verify
go test ./examples/files/...
```

`add` prints the tree digest and `commit` the commit digest in bare hex (`cas.Digest.String`), `log` prints the printable `sha256:…` form, `stats` a `N objects, M bytes` summary, and `audit` one `state hash` line per object plus a `verified/orphaned/corrupt/unverified` count.
