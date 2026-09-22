package refs

import "errors"

// Sentinel errors for the refs package, checked with errors.Is (library-design
// §2). Each is wrapped with the offending name via fmt.Errorf("%w: ...", ...)
// so a caller gets both the stable sentinel and the concrete context.
var (
	// ErrNotFound reports a ref name (or reflog) that does not exist.
	ErrNotFound = errors.New("refs: not found")
	// ErrAmbiguous reports a name prefix passed to Resolve that matches more
	// than one stored ref name.
	ErrAmbiguous = errors.New("refs: ambiguous name")
	// ErrInvalidName reports a ref name that ValidateName rejects.
	ErrInvalidName = errors.New("refs: invalid name")
)
