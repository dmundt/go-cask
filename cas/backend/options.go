// Package backend provides shared helpers for Backend implementations.
//
// Concrete backends (fs, memory, packfs, s3, ...) each declare their own
// configuration struct and their own Option type — a func over that concrete
// struct. There is no shared Option type: a cross-backend option is therefore a
// compile-time error instead of a silently ignored one (AGENTS.md principle 4,
// no `any` in the exported API).
//
// What does live here is the streaming plumbing every backend needs:
// ContextReader, WriteAll, ReadAll, and ReadPayload.
package backend
