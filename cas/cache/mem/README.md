# mem — cached store wrapper

Package `mem` provides the in-memory cached store of the generic `cas` core.

Clause is `memory`, which `cas/backend/mem` also declares, so this repository imports it as
`cachemem` and the backend as `backmem` (`cas/AGENT.md`); no file imports either one unaliased.

Adds lazy loading and caching semantics on top of a typed store, so repeated reads reuse
already-loaded values without re-decoding the object. Read-performance optimization, not a change to
object identity.

```go
cached := cachemem.New(store)
```
