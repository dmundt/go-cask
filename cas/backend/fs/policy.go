package fs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateBase ensures a store root looks usable and is not an empty or parent-
// traversal path. This is a helper for app and CLI callers that need a safe
// filesystem root without changing the core store semantics.
func ValidateBase(base string) error {
	if strings.TrimSpace(base) == "" {
		return fmt.Errorf("cas: empty store base")
	}
	if filepath.Clean(base) == "." || filepath.Clean(base) == string(filepath.Separator) {
		return fmt.Errorf("cas: store base %q is not a writable repo directory", base)
	}
	return nil
}

// EnsureBase creates the root directory for a store when it is missing.
func EnsureBase(base string) error {
	if err := ValidateBase(base); err != nil {
		return err
	}
	return os.MkdirAll(base, 0o755)
}

// CleanupTemp removes any temporary files beneath base that match the backend's
// *.tmp convention. It is advisory and safe to run on an otherwise valid store.
func CleanupTemp(base string) error {
	if err := ValidateBase(base); err != nil {
		return err
	}
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".tmp") {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	})
}
