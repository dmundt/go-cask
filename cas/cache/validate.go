package cache

import "fmt"

// ValidateMaxSize checks a cache size limit and keeps cache-specific validation
// in one place without making higher-level packages depend on a specific cache
// implementation.
func ValidateMaxSize(maxSize int, name string) error {
	if maxSize <= 0 {
		return fmt.Errorf("%s: maxSize must be > 0, got %d", name, maxSize)
	}
	return nil
}
