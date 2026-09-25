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
