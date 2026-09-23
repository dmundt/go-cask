package cas

import (
	"context"
	"errors"
	"fmt"
)

// Report summarizes a VerifyAll run: how many objects were checked and which
// digests failed verification.
type Report struct {
	// Checked is the number of objects VerifyAll examined.
	Checked int
	// Bad lists digests whose stored bytes no longer match their digest
	// (ErrDigestMismatch). A digest that failed to read for any other
	// reason aborts VerifyAll instead of being added here — Bad reports
	// confirmed corruption, not "could not check".
	Bad []Digest
}

// VerifyAll re-reads every object raw.List reports and recomputes its digest
// with hasher, using only the minimal Backend interface (List, Get) — so it
// works against any backend, including one that implements no maintenance
// methods of its own. A concrete backend may still expose a faster
// backend-native Verify; VerifyAll is the portable fallback every backend
// supports (see Capabilities.Verify, which is always true).
func VerifyAll(ctx context.Context, backend Backend, hasher Hasher) (*Report, error) {
	if backend == nil {
		return nil, fmt.Errorf("cas: verify all: nil backend")
	}
	if hasher == nil {
		return nil, fmt.Errorf("cas: verify all: nil hasher")
	}
	digests, err := backend.List(ctx)
	if err != nil {
		return nil, err
	}
	report := &Report{}
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Checked++
		if err := Verify(ctx, backend, d, hasher); err != nil {
			if errors.Is(err, ErrDigestMismatch) {
				report.Bad = append(report.Bad, d)
				continue
			}
			return report, fmt.Errorf("cas: verify all %s: %w", d, err)
		}
	}
	return report, nil
}

// Verifier performs integrity checks against a Backend using the caller's
// Hasher. The storage model remains unchanged: identity and byte storage stay in
// Digest/Backend, while integrity validation is a separate maintenance layer.
type Verifier struct {
	backend Backend
	hasher  Hasher
}

// NewVerifier creates a Verifier for backend and hasher.
func NewVerifier(backend Backend, hasher Hasher) *Verifier {
	return &Verifier{backend: backend, hasher: hasher}
}

// Verify re-reads the object at d and recomputes its digest with the verifier's
// hasher. A Verifier is used through NewVerifier, which always returns a usable
// value, so a nil receiver is a programming error rather than a runtime state
// this method needs to report.
func (v *Verifier) Verify(ctx context.Context, d Digest) error {
	return Verify(ctx, v.backend, d, v.hasher)
}

// Verify re-reads the object at d and recomputes its digest with the supplied
// hasher. The backend does not own integrity checking; it stores backend bytes
// and exposes them to an explicit verification layer.
func Verify(ctx context.Context, backend Backend, d Digest, hasher Hasher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend == nil {
		return fmt.Errorf("cas: verify: nil backend")
	}
	if err := CheckDigest(d, "cas: verify"); err != nil {
		return err
	}
	if hasher == nil {
		return fmt.Errorf("cas: verify: nil hasher")
	}
	if err := hasher.Validate(d); err != nil {
		return err
	}
	rc, err := backend.Get(ctx, d)
	if err != nil {
		return err
	}

	actual, err := hasher.Digest(rc)
	if err != nil {
		_ = rc.Close()
		return fmt.Errorf("cas: verify read: %w", err)
	}
	if err := rc.Close(); err != nil {
		return fmt.Errorf("cas: verify close: %w", err)
	}
	if !actual.Equal(d) {
		return fmt.Errorf("%w: %s", ErrDigestMismatch, d)
	}
	return nil
}
