# lru — bounded LRU cache

Size-bounded LRU wrapper over a typed `Store[T]`: keeps recently used entries hot, evicts
the least-recently-used object at the cap. Optional optimization layer, never part of the
content-addressing model — retention is a performance policy, not a semantic requirement,
and it must not change object identity or storage semantics.

```go
cache, err := lru.New(store, 1024)
```
