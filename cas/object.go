package cas

// Object[T] is a self-describing, reference-aware typed object. T is the
// concrete type (e.g. *Blob). Implementations know how to serialize
// themselves and which hashes they reference; the generic core never
// interprets types or references — References() is the single source of
// truth for traversal, preloading, and GC reachability.
//
// Serialize returns the object's stored form: the self-describing envelope
// {"type": "<type>@<major>", "data": "<base64 codec payload>"} (see
// Store.Put). Deserialize parses that envelope and returns T. Type() must
// return a versioned type name "<type>@<major>" (e.g. "commit@1") so several
// object-model majors can coexist in one store and the version travels with
// the bytes.
type Object[T any] interface {
	Type() string       // versioned type name, e.g. "blob@1"
	References() []Hash // hashes this object points to; may be nil
	Serialize() ([]byte, error)
	Deserialize(data []byte) (T, error) // returns T — no casts for callers
}
