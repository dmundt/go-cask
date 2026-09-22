package cas

import (
	"context"
	"fmt"
)

// Verifier performs integrity checks against a Backend using the caller's
// Hasher. The storage model remains unchanged: identity and byte storage stay in
// Digest/Backend, while integrity validation is a separate maintenance layer.
type Verifier struct {
	raw    Backend
	hasher Hasher
}

// NewVerifier creates a Verifier for raw and hasher.
func NewVerifier(raw Backend, hasher Hasher) *Verifier {
	return &Verifier{raw: raw, hasher: hasher}
}

// Verify re-reads the object at d and recomputes its digest with the verifier's
// hasher. A Verifier is used through NewVerifier, which always returns a usable
// value, so a nil receiver is a programming error rather than a runtime state
// this method needs to report.
func (v *Verifier) Verify(ctx context.Context, d Digest) error {
	return Verify(ctx, v.raw, d, v.hasher)
}

// Verify re-reads the object at d and recomputes its digest with the supplied
// hasher. The backend does not own integrity checking; it stores raw bytes and
// exposes them to an explicit verification layer.
func Verify(ctx context.Context, raw Backend, d Digest, hasher Hasher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if raw == nil {
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
	rc, err := raw.Get(ctx, d)
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
