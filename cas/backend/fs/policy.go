package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ValidateBase reports whether base is usable as a store root. It rejects the
// shapes that would make a sweep touch something other than the caller's own
// store: an empty or whitespace-only path, "." and the bare filesystem root,
// a parent-traversal path ("..", "../x", "a/../.."), and a volume root such as
// "C:\" or "C:". A nested relative or absolute directory below that root is
// accepted. It performs no I/O, so it needs no context.
//
// New runs it before creating anything, so a caller that goes through the
// constructor never needs it. It stays exported for the caller that owns the
// base path itself — a CLI flag, a config value, a path built from user input —
// and wants to reject it before opening a backend (or before creating the
// directory a non-fs backend needs).
func ValidateBase(base string) error {
	if strings.TrimSpace(base) == "" {
		return fmt.Errorf("cas: empty store base")
	}
	clean := filepath.Clean(base)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("cas: store base %q is not a writable repo directory", base)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("cas: store base %q is a parent-traversal path", base)
	}
	// A volume root has nothing but its volume left after Clean; on Windows
	// Clean("C:") is "C:.", so "." counts as an empty remainder too.
	if rest := strings.TrimPrefix(clean, filepath.VolumeName(clean)); rest == "" || rest == "." || rest == string(filepath.Separator) {
		return fmt.Errorf("cas: store base %q is a volume root, not a repo directory", base)
	}
	return nil
}

// ErrUnsafeTarget reports a path inside a store base that is not what the
// store's own layout claims it is: a symbolic link standing where the backend
// expected a directory or a file of its own, or a non-directory where a
// directory is needed. os.MkdirAll, os.OpenFile, os.WriteFile and os.Stat all
// follow a link, so without this refusal an entry planted inside the base
// redirects a write (or a read) outside the base the store owns (cas-core
// §4.4).
var ErrUnsafeTarget = errors.New("cas: unsafe store path")

// ValidateDir reports whether dir is usable as a directory inside base: every
// component of dir below base that already exists must be a real directory, not
// a symbolic link. It is the check that keeps os.MkdirAll — which follows a
// link — from building a store tree outside the base.
//
// It performs I/O (one Lstat per existing component) but no mutation, and it
// runs before the caller creates anything, so a link it reports has redirected
// nothing yet. A component that does not exist is accepted: a path that is not
// there cannot be a link, and the caller creates the rest. dir MUST be inside
// base; base itself, an empty relative path and anything above the base are
// ErrUnsafeTarget. The base is deliberately not inspected — whether the
// directory the caller named is itself a link is the caller's decision, made
// once where the path is resolved (internal/store.ResolveBase), while this rule
// is about the entries INSIDE the base.
func ValidateDir(base, dir string) error {
	return validateChain(base, dir, true)
}

// ValidateFile is ValidateDir for a file path, with the entry at path itself
// added to the rule: a symbolic link there is ErrUnsafeTarget. Every read of
// the object goes through that name, so a planted link silently serves the
// bytes of whatever it points at; the write path refuses it rather than
// publishing over it.
//
// Any other kind of entry at path (a directory, a device, a socket) is left to
// the caller's own operation: the atomic rename cannot replace a directory and
// reports its own failure, and a link is the only shape that redirects the
// write or the read elsewhere. A path that does not exist is accepted — the
// store is about to create it.
//
// A file the store already published is a regular file, so an idempotent
// re-Put passes: this is a shape check on the store's own layout, not an
// ownership check (cas-core §4.4).
func ValidateFile(base, path string) error {
	return validateChain(base, path, false)
}

// validateChain is the one implementation behind ValidateDir and ValidateFile:
// it Lstats the components of path below base in order and reports the first
// entry that is a link or is not the directory the layout needs. wantDir selects
// whether the LAST component must be a directory too (ValidateDir) or may be a
// file the caller is about to create (ValidateFile); every component before it
// must be a real directory either way. A component that does not exist ends the
// walk successfully: nothing below an absent entry can be a link, and the caller
// creates it next.
//
// The component paths are built by concatenation rather than filepath.Join:
// Put runs this on every call, and Join's cleaning allocates ~270 B per
// component, which the write path's allocation guard (alloc_test.go,
// go-cask#368) does not have room for. filepath.Rel still supplies the
// components, so a caller's path is normalized exactly as Join would.
func validateChain(base, path string, wantDir bool) error {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return fmt.Errorf("%w: %s is not inside the store base %s: %w", ErrUnsafeTarget, path, base, err)
	}
	if rel == "." {
		return fmt.Errorf("%w: %s is the store base itself, not an entry inside it", ErrUnsafeTarget, path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %s is outside the store base %s", ErrUnsafeTarget, path, base)
	}
	sep := string(filepath.Separator)
	prefix := base
	if prefix != "" && !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	end := 0
	for {
		i := strings.IndexByte(rel[end:], byte(filepath.Separator))
		last := i < 0
		if last {
			end = len(rel)
		} else {
			end += i
		}
		cur := prefix + rel[:end]
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // absent: there is nothing here to follow
			}
			return fmt.Errorf("cas: stat store path %s: %w", cur, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(cur)
			if readErr != nil {
				target = "an unreadable target"
			}
			return fmt.Errorf("%w: %s is a symbolic link to %s", ErrUnsafeTarget, cur, target)
		}
		// Every component before the last must be a real directory, and so must
		// the last one when a directory is what the caller needs. The last
		// component of a file path is not judged here: it is not a link, and the
		// caller's own operation decides what anything else at that name means.
		if (!last || wantDir) && !info.IsDir() {
			return fmt.Errorf("%w: %s is not a directory (mode %s)", ErrUnsafeTarget, cur, info.Mode())
		}
		if last {
			return nil
		}
		end++ // step over the separator before the next component
	}
}

// EnsureBase creates the root directory for a store when it is missing. It
// honors ctx: cancellation is checked before the base is validated and again
// while the directory tree is created.
//
// New already validates and creates the base, so a caller that opens an
// fs.Backend does not need this. It is the caller-facing pre-flight for a base
// another backend receives directly, or for creating the directory before an
// operation that needs it to exist.
func EnsureBase(ctx context.Context, base string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateBase(base); err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("cas: create store base: %w", err)
	}
	return ctx.Err()
}

// CleanupTemp removes any temporary files beneath base that match the backend's
// temp-file convention (isTempFile). It is advisory and safe to run on an
// otherwise valid store; ctx is honored before each entry so a large sweep can
// be canceled. A file that disappears during the sweep (a concurrent sweep, a
// crash cleanup) is not an error.
//
// It is CleanTemp(ctx, base, 0) — every matching file, whatever its age, with
// the count discarded. It is the same sweep Backend.Clean runs, for a caller
// that holds only the base path — a maintenance step that reclaims crash
// leftovers before a store is opened, or after a process died mid-write. It
// removes every matching file beneath base, so base MUST be the caller's own
// store directory (ValidateBase is applied first for that reason);
// Backend.Clean is the same sweep through an already-open backend.
func CleanupTemp(ctx context.Context, base string) error {
	_, err := CleanTemp(ctx, base, 0)
	return err
}

// CleanTemp removes every temporary file (isTempFile) beneath root older than
// olderThan and reports how many it removed; olderThan <= 0 removes every
// matching file regardless of age. It is the sweep Backend.Clean runs, exported
// for a caller that owns another tree under the same convention — packfs.Clean
// sweeps its pack directory, `<base>/packs`, with it — so the walk, the age
// cutoff and the tolerated failures exist once and cannot drift between the two
// trees.
//
// CleanupTemp is the same sweep with the age fixed at 0 and no count. Both
// remove every matching file beneath the directory they are handed, so root MUST
// be the caller's own store directory or its own scratch tree; ValidateBase is
// applied first for that reason. ctx is honored before the walk and before each
// entry. A root that vanished is not an error (there is nothing to sweep), while
// every other walk or removal failure is returned.
func CleanTemp(ctx context.Context, root string, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := ValidateBase(root); err != nil {
		return 0, err
	}
	return cleanTemp(ctx, root, olderThan, nil)
}

// cleanTemp is the one temp-file sweep: Backend.Clean and CleanTemp both reach
// it, so the walk callback, the age cutoff and the tolerated failures are
// written once. walk is the Backend's scripted-walk seam; nil means
// filepath.WalkDir, the fallback that also keeps a Backend a test builds by hand
// — and the zero value — safe to sweep.
func cleanTemp(ctx context.Context, root string, olderThan time.Duration, walk func(string, fs.WalkDirFunc) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if walk == nil {
		walk = filepath.WalkDir
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := walk(root, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // entry vanished during concurrent cleanup/write
			}
			return err
		}
		if d.IsDir() || !isTempFile(d.Name()) {
			return nil
		}
		if olderThan > 0 {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if fi.ModTime().After(cutoff) {
				return nil
			}
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed by a concurrent sweep
			}
			return err
		}
		removed++
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return removed, nil // the directory itself is gone: nothing to clean
		}
		return removed, fmt.Errorf("cas: clean: %w", err)
	}
	return removed, nil
}
