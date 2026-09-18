# Backends

A backend is the storage engine for raw bytes.

## Common backends

- filesystem backend — durable, local, and easy to reason about
- in-memory backend — useful for tests and examples
- custom backends — can plug into the same `Backend` contract

## Contract

A backend should support:

- storing bytes under a digest
- retrieving bytes by digest
- checking whether an object exists
- listing objects
- reporting stats
- deleting an object when needed

## Design goal

The storage backend does not need to know about object types, JSON, codec wrappers, or application invariants. It is intentionally simple: put bytes, get bytes, list them, and report storage metadata.
