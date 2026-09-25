package refs

// This file records the refs branches no deterministic test reaches, with each
// reason written down (testing-strategy.md §5). Every one of them is a defensive
// guard in code that has no seam to inject a failure:
//
//   - refs.go List's `return rerr` when filepath.Rel fails for a path the
//     WalkDir callback received. WalkDir derives every callback path from the
//     root it was given, and Rel only fails when it cannot express one of them
//     relative to the other (a volume change on Windows, or a root that is
//     itself relative and empty). The existing List tests cover the reachable
//     walk failures: an unreadable ref, an unreadable directory, a corrupt
//     value and the reserved log entry.
//
//   - refs.go appendLog's `return err` when writing the reflog line fails. The
//     path is opened with os.OpenFile(path, O_CREATE|O_WRONLY|O_APPEND), and
//     the only path shapes a test can stage that would reject the write — the
//     path is a directory, or its parent is not a directory — already fail at
//     that open (TestSetAndDeleteReportReflogFailures covers both), so the
//     write branch needs an open handle that rejects a write, which this
//     package exposes no seam for.
//
//   - refs.go writeFileAtomic's three failure returns after the temp file
//     exists: a short write (refs.go:492), a failing fsync (refs.go:497) and a
//     failing close (refs.go:502). The function is unexported and takes no file
//     seam, and the temp file lives in a directory the test owns, so the only
//     way to fail those steps is a real I/O fault (ENOSPC, EIO) that a test
//     cannot stage portably.
//
//   - refs.go syncParentDir's Windows early return (refs.go:517). It is
//     compiled into every build, but the coverage gate measures on Linux only
//     (testing-strategy §5, "Platform-split packages are measured on Linux
//     only"), and on Linux the branch is false by construction.
