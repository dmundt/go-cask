package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateBase reports whether base is usable as a store root. It rejects the
// shapes that would make a sweep touch something other than the caller's own
// store: an empty or whitespace-only path, "." and the bare filesystem root,
// a parent-traversal path ("..", "../x", "a/../.."), and a volume root such as
// "C:\" or "C:". A nested relative or absolute directory below that root is
// accepted. It performs no I/O, so it needs no context.
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
func CleanupTemp(ctx context.Context, base string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateBase(base); err != nil {
		return err
	}
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !isTempFile(d.Name()) {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
}
