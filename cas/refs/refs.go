// Package refs implements the mutable half of a content-addressable store:
// named pointers ("refs") to a cas.Digest, with atomic writes and an
// append-only reflog per name (go-cask#135). The immutable object graph
// already lives in cas.Store[T]; refs are how an application spells "which
// revision is current" without re-implementing atomic pointer writes, name
// validation, and prefix resolution on top of it (the gap examples/files and
// go-context's internal/artifacts/refs.go each closed independently).
//
// A Store keeps one small file per ref under a directory the caller owns
// (conventionally "refs/" beside a store's "objects/" — the two are
// independent trees and Open takes whichever directory the caller wants refs
// rooted at). Each Set call replaces the ref's value file atomically
// (temp file + fsync + rename, plus a best-effort parent-directory fsync on
// POSIX) and appends one line to the ref's reflog, so History/Previous never
// need to scan the object store the way a peek-every-object approach does.
package refs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// logSubdir holds the reflog files, one per ref name, mirroring the value
// files' directory structure but rooted one level below the ref values
// themselves. Ref names cannot start with "." (ValidateName), so this
// reserved, dot-prefixed name never collides with a stored ref.
const logSubdir = ".log"

// Ref pairs a stored ref name with its current digest, as returned by List.
type Ref struct {
	// Name is the ref's full name (forward-slash separated, validated by
	// ValidateName).
	Name string
	// Digest is the ref's current value.
	Digest cas.Digest
}

// Entry is one reflog record: the digest a Set/Delete moved the ref to
// (Digest, absent for a Delete), what it replaced (Old, absent if this was
// the ref's first Set), and when the change was recorded.
type Entry struct {
	// Digest is the value the ref was set to; absent (IsZero) records a
	// Delete.
	Digest cas.Digest
	// Old is the value the ref held immediately before this entry; absent if
	// the ref did not exist yet.
	Old cas.Digest
	// Time is when the change was appended to the log.
	Time time.Time
}

// Store manages named, mutable pointers to cas.Digest values ("refs") under
// one directory, each with an atomically-written current value and an
// append-only reflog. The zero value is not usable; construct one with Open.
type Store struct {
	dir string
	mu  sync.Mutex // serializes Set/Delete within one process
	now func() time.Time
}

// Option configures a Store constructed by Open.
type Option func(*Store)

// WithClock overrides the reflog's time source (time.Now by default), for
// deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// Open creates a Store rooted at dir, creating the directory if needed. dir
// is a plain filesystem directory the caller owns and dedicates to refs (for
// example filepath.Join(storeDir, "refs")); it is unrelated to any
// cas.Backend base directory.
func Open(dir string, opts ...Option) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("refs: open: empty directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("refs: open: %w", err)
	}
	s := &Store{dir: dir, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// windowsReservedChars are the characters never allowed in a stored path
// component: <>:"|?* (Win32's reserved set) plus backslash. Backslash is
// rejected unconditionally, on every OS, so "/" is the name's only separator
// everywhere — accepting backslash only on Windows (where filepath.ToSlash
// would otherwise silently treat it as a separator) would let the same name
// resolve to a different segment count depending on the host OS. Rejecting
// these unconditionally (not just when running on Windows) keeps a name
// portable across whichever OS eventually opens this refs directory.
const windowsReservedChars = `<>:"|?*\`

// windowsReservedNames are the MS-DOS device names Win32 reserves in every
// directory, regardless of extension (e.g. "con", "con.txt", and "CON" are
// all reserved).
var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com0": true, "com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt0": true, "lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateName rejects a ref name that cannot be safely stored as a file
// path on both POSIX and Windows: empty, an absolute path, a "." or ".."
// segment, a segment starting with "." (also reserves the internal
// logSubdir from ever colliding with a stored name), a segment ending in
// ".lock" (the lock-file suffix Git-style atomic writers use), a segment
// ending in a space or a "." (Win32 silently trims a trailing space/dot from
// a path component, so "a " and "a" would otherwise collide on Windows),
// a segment containing any of windowsReservedChars, a segment that is a
// Win32 reserved device name (CON, PRN, AUX, NUL, COM0-9, LPT0-9, with or
// without an extension), a control character (including NUL), or the DEL
// character. Every one of these beyond the base "."/".."/logSubdir rules was
// found by FuzzValidateName exercising real Set/Get round-trips on Windows.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %q contains a control character", ErrInvalidName, name)
		}
	}
	clean := name
	if strings.HasPrefix(clean, "/") || filepath.IsAbs(name) {
		return fmt.Errorf("%w: %q is an absolute path", ErrInvalidName, name)
	}
	for part := range strings.SplitSeq(clean, "/") {
		switch {
		case part == "":
			return fmt.Errorf("%w: %q has an empty path segment", ErrInvalidName, name)
		case part == ".", part == "..":
			return fmt.Errorf("%w: %q contains a %q segment", ErrInvalidName, name, part)
		case strings.HasPrefix(part, "."):
			return fmt.Errorf("%w: %q segment %q must not start with '.'", ErrInvalidName, name, part)
		case strings.HasSuffix(part, ".lock"):
			return fmt.Errorf("%w: %q segment %q must not end with '.lock'", ErrInvalidName, name, part)
		case strings.HasSuffix(part, " "), strings.HasSuffix(part, "."):
			return fmt.Errorf("%w: %q segment %q must not end with a space or '.'", ErrInvalidName, name, part)
		case strings.ContainsAny(part, windowsReservedChars):
			return fmt.Errorf("%w: %q segment %q contains a reserved character (%s)", ErrInvalidName, name, part, windowsReservedChars)
		case windowsReservedNames[strings.ToLower(strings.SplitN(part, ".", 2)[0])]:
			return fmt.Errorf("%w: %q segment %q is a reserved device name", ErrInvalidName, name, part)
		}
	}
	return nil
}

func (s *Store) valuePath(name string) string {
	return filepath.Join(s.dir, filepath.FromSlash(name))
}

func (s *Store) logPath(name string) string {
	return filepath.Join(s.dir, logSubdir, filepath.FromSlash(name))
}

// Get returns name's current digest, or ErrNotFound if it has never been Set
// (or was Deleted).
func (s *Store) Get(ctx context.Context, name string) (cas.Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	return s.readValue(name)
}

func (s *Store) readValue(name string) (cas.Digest, error) {
	data, err := os.ReadFile(s.valuePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("refs: get %s: %w", name, err)
	}
	d, err := cas.ParseDigest(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("refs: get %s: %w", name, err)
	}
	return d, nil
}

// Set points name at d, atomically (temp file + fsync + rename), and appends
// one entry to name's reflog recording the transition from its previous
// value (absent, if name did not exist) to d.
func (s *Store) Set(ctx context.Context, name string, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "refs: set"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.readValue(name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := writeFileAtomic(s.valuePath(name), []byte(d.String()+"\n")); err != nil {
		return fmt.Errorf("refs: set %s: %w", name, err)
	}
	if err := s.appendLog(name, old, d); err != nil {
		return fmt.Errorf("refs: set %s: append log: %w", name, err)
	}
	return nil
}

// Delete removes name's current value, appending a tombstone entry (Digest
// absent) to its reflog first. Deleting a name that does not exist is a
// no-op, matching Backend.Delete's idempotence. The reflog itself is kept
// (Log/Previous keep working for a deleted name), mirroring how Git keeps a
// branch's reflog after the branch is deleted until it is pruned.
func (s *Store) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.readValue(name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.appendLog(name, old, nil); err != nil {
		return fmt.Errorf("refs: delete %s: append log: %w", name, err)
	}
	if err := os.Remove(s.valuePath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("refs: delete %s: %w", name, err)
	}
	return nil
}

// List returns every stored ref, sorted by name.
func (s *Store) List(ctx context.Context) ([]Ref, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []Ref
	err := filepath.WalkDir(s.dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(s.dir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if top == logSubdir {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if de.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		name := filepath.ToSlash(rel)
		d, gerr := s.readValue(name)
		if gerr != nil {
			return fmt.Errorf("refs: list: %s: %w", name, gerr)
		}
		out = append(out, Ref{Name: name, Digest: d})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve looks up nameOrPrefix: an exact stored name wins outright; failing
// that, a prefix that matches exactly one stored name resolves to it.
// Resolve returns ErrNotFound for no match and ErrAmbiguous — naming every
// candidate — for more than one.
func (s *Store) Resolve(ctx context.Context, nameOrPrefix string) (Ref, error) {
	if err := ctx.Err(); err != nil {
		return Ref{}, err
	}
	if nameOrPrefix == "" {
		return Ref{}, fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	all, err := s.List(ctx)
	if err != nil {
		return Ref{}, err
	}
	for _, r := range all {
		if r.Name == nameOrPrefix {
			return r, nil
		}
	}
	var matches []Ref
	for _, r := range all {
		if strings.HasPrefix(r.Name, nameOrPrefix) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return Ref{}, fmt.Errorf("%w: %s", ErrNotFound, nameOrPrefix)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return Ref{}, fmt.Errorf("%w: %q matches %s", ErrAmbiguous, nameOrPrefix, strings.Join(names, ", "))
	}
}

// Roots returns the current digest of every stored ref, skipping any that
// somehow hold the absent digest. It is the root set Backend.GC/Backend.Prune
// need — expand it with cas.Reachable before calling either (cas-core §4.11,
// consistency.md §4).
func (s *Store) Roots(ctx context.Context) ([]cas.Digest, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	roots := make([]cas.Digest, 0, len(all))
	for _, r := range all {
		if !r.Digest.IsZero() {
			roots = append(roots, r.Digest)
		}
	}
	return roots, nil
}

// Previous returns the digest name pointed at immediately before its most
// recent Set, or ErrNotFound if name has no reflog entry with a prior value
// (never Set, or its first Set).
func (s *Store) Previous(ctx context.Context, name string) (cas.Digest, error) {
	entries, err := s.Log(ctx, name, 1)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || entries[0].Old.IsZero() {
		return nil, fmt.Errorf("%w: %s has no previous revision", ErrNotFound, name)
	}
	return entries[0].Old, nil
}

// Log returns name's reflog, newest first, capped at limit entries (0 or
// negative returns every entry). A name with no reflog yet returns (nil,
// nil), not an error. Log answers from the ref's own log file only — its
// cost does not depend on how many objects or other refs the store holds.
func (s *Store) Log(ctx context.Context, name string, limit int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.logPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("refs: log %s: %w", name, err)
	}
	entries := parseLog(data)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// appendLog records one Set/Delete transition. The append is not wrapped in
// the same atomic rename as the value write: a crash between the two leaves
// the value updated but the log tail missing the newest entry, which Log's
// lenient parser and the value file itself (still the source of truth for
// Get) both tolerate — the reflog is a convenience history, not the record
// of truth Get/Resolve rely on.
func (s *Store) appendLog(name string, old, next cas.Digest) error {
	path := s.logPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := fmt.Sprintf("%d\t%s\t%s\n", s.now().UnixNano(), next.String(), old.String())
	if _, err := f.Write([]byte(line)); err != nil {
		return err
	}
	return f.Sync()
}

// parseLog parses appendLog's line format, oldest first, silently dropping
// any line that is not exactly three tab-separated fields with a valid
// timestamp and digests — a torn tail left by a process that crashed
// mid-append is recoverable (the rest of the log is used), never fatal.
func parseLog(data []byte) []Entry {
	lines := strings.Split(string(data), "\n")
	entries := make([]Entry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		ns, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		next, err := parseLogDigest(parts[1])
		if err != nil {
			continue
		}
		old, err := parseLogDigest(parts[2])
		if err != nil {
			continue
		}
		entries = append(entries, Entry{Digest: next, Old: old, Time: time.Unix(0, ns)})
	}
	return entries
}

// parseLogDigest parses one reflog field: "" is the absent digest (a Delete's
// Digest, or a first Set's Old), anything else must be a valid digest.
func parseLogDigest(s string) (cas.Digest, error) {
	if s == "" {
		return nil, nil
	}
	return cas.ParseDigest(s)
}

// writeFileAtomic replaces path's content with data via temp file + fsync +
// rename, plus a best-effort parent-directory fsync on POSIX (Windows
// directories cannot be fsynced and do not need it: MoveFileEx there is
// already durable enough for this package's purposes).
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		// A previous writer crashed before renaming; the leftover is unusable
		// (List already ignores ".tmp" files), so replace it and retry once.
		_ = os.Remove(tmp)
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncParentDir(path)
}

// syncParentDir fsyncs the directory containing path, so the rename in
// writeFileAtomic survives a crash, not just the file content. A no-op on
// Windows, which cannot open a directory for Sync.
func syncParentDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
