# sidecar

Optional per-object checksum beside a store's bytes, so a cheap check runs over a store whose address is a strong hash. A `cas.Backend` decorator: the address stays the authoritative identity, the record never enters the hashed bytes, and deleting the record directory loses the cheap check, never an object.

It is the fifth extension shape (extensions §2.1) and the inverse of [the checksum hashers](../README.md): those implement `cas.Hasher` for a store deliberately *addressed* by a checksum, while `sidecar` records one *beside* objects addressed by something else. Normative contract: `operations.md` §6. `Get`, `Exists`, `List` and `Stats` delegate untouched, so the decorator composes with `cas.New`, `gitlike.NewRepository` and `internal/store.Open` unchanged.

## Layout

```text
<base>/<fan-out>/<hex>        object bytes (the backend's own layout)
<base>/.meta/<hex>.json       the record for that digest
<base>/.meta/<hex>.<n>.tmp    atomic-write scratch, reclaimed by the backend's Clean
```

`<base>` is what the backend reports as `BasePath()`: the path passed to `fs.New`, or, for `packfs`, the loose tree at `<base>/loose` — the directory its `List`, `Stats` and `Clean` operate on. `.meta` is the one sanctioned resident under a store's base (cas-core §4.4), because its files are neither digest-named (the `.json` suffix keeps them out of `List`/`Stats`) nor free `*.tmp` scratch.

## Typical use

```go
backend, _ := fs.New("store/objects")
rec, _ := sidecar.New(backend,
    sidecar.WithChecksum(crc32.Name, crc32.New()), // opt-in: no option, no record
    sidecar.WithDirSync(),                         // optional durability for the rename
)
store := cas.New(rec, json.New[*Blob](), sha256.New()) // rec is a cas.Backend
```

Records are written on `Put` and removed on `Delete`. Validating and maintaining:

```go
// One object: compares the stored bytes with the record, never with the address.
err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d)

// Every recorded object: Bad lists checksum failures, Unreadable lists records
// that cannot be read at all (the pass still checks the rest of the store), and
// Unrecorded lists objects with no record (unchecked, never corrupt).
report, err := rec.Verifier(crc32.Name, crc32.New()).VerifyAll(ctx)

// After a sweep: remove the records of objects that are gone, report the rest.
reconciled, err := rec.Reconcile(ctx)
```

From the CLI, `cask verify --checksums [-checksum crc32|adler32|crc64] <hash>|--all` reads the records, and `cask gc`/`cask prune` reconcile them after their sweep.

## Errors

- Absent record: `cas.ErrNotFound` (wrapped as `sidecar.ErrUnrecorded`) — an unchecked object, never corruption; the lesson of the deleted `examples/files` `.crc32` sidecar (go-cask#196).
- Record written by another checksum: `sidecar.ErrChecksumAlgorithm`, not corruption — crc32 and adler32 are both four bytes wide, so width alone cannot tell a reader change from damage.
- Checksum or size disagreement: `cas.ErrCorrupt` (wrapped) — as is a record that cannot be parsed, is the wrong version, or exceeds the read cap, never a silent skip.
- Record unreadable at all: reported, not fatal — `VerifyReport.Unreadable` names it and `VerifyAll` keeps checking the rest, so one damaged record cannot report the whole store unchecked. Single-object `Verify`: `cas.ErrCorrupt`.
- The read cap bounds the writer too: a record that would exceed it is written without the optional `type`/`codec` fields, and one that still does not fit is refused with `sidecar.ErrRecordTooLarge`, publishing nothing. A writer never produces a record its own reader would refuse.
- A `.json` name in `.meta` that is not a digest is not a record: `Keys`/`Reconcile` skip it and `Reconcile` reports it in `ReconcileReport.Foreign`, so one foreign file cannot abort a reconciliation.
- A write whose reader is not drained to EOF fails loudly and writes no record: a checksum over partial bytes is worse than none.

## Policy

- Off by default: nothing in the core, the CLI or the viewer writes a record unless a caller asks for one by wrapping a backend.
- The checksum is over the **stored bytes**, not a logical payload; a record is a maintenance signal and is never repaired automatically.
- Ordering is object first, record second: a crash in between leaves an unchecked object, which `Reconcile` closes.
- Quarantine, alerting, a logical-payload checksum and a `references` field are deliberately not part of v1 (operations §6.5).
