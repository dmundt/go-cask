// On-demand integrity checks: one object, or the whole store, with the finding
// recorded in the session so the inspector can replay it.

package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dmundt/go-cask/cas"
)

// verifyAllFragment verifies every stored object and records each result in the
// session. It is the bulk counterpart of verifyFragment: one audit line per
// object would flood the log, so it audits the sweep as a single event with
// counts.
func (s *Server) verifyAllFragment(w http.ResponseWriter, r *http.Request) {
	digests, err := s.store.List(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	id := sessionID(r)
	verified, corrupt := 0, 0
	for _, h := range digests {
		if err := r.Context().Err(); err != nil {
			return
		}
		if err := s.store.Verify(r.Context(), h, s.cfg.Hasher); err != nil {
			outcome := s.describeVerifyFailure(r.Context(), h, err)
			outcome.Integrity = "corrupt"
			outcome.IntegrityLabel = integrityLabel("corrupt")
			s.sessions.setVerification(id, h.String(), "corrupt", outcome)
			corrupt++
			continue
		}
		s.sessions.setVerification(id, h.String(), "verified", verifiedOutcome(h))
		verified++
	}
	slog.Info("viewer audit", "action", "object.verify-all", "session", sessionHandle(id), "objects", len(digests), "verified", verified, "corrupt", corrupt)
	w.Header().Set("HX-Trigger", "object-status-updated")
	// The label stays "Verify": the per-object status cells already carry the
	// outcome, so a count on the control would only duplicate them.
	s.render(w, "verify-all-button", verifyAllState{
		CSRF:  s.csrfFor(r),
		Label: "Verify",
	})
}

// verifyAllState backs the top-bar Verify control.
type verifyAllState struct {
	CSRF  string
	Label string
}

// actionOutcome is the structured result of an object action (verification).
// It replaces the raw error string: a sentinel classifies the failure and the
// recomputed digest shows the operator exactly how the bytes diverged.
type actionOutcome struct {
	OK       bool
	Headline string
	Summary  string
	Expected string
	Actual   string
	Detail   string
	Checked  string
	// Integrity and IntegrityLabel refresh the inspector's Integrity row out
	// of band; they stay empty for actions that leave no object behind.
	Integrity      string
	IntegrityLabel string
}

func (s *Server) verifyFragment(w http.ResponseWriter, r *http.Request) {
	h, ok := s.parseDigest(w, r)
	if !ok {
		return
	}
	// Every operator action is audit-logged with the acting session, the
	// affected object, and the result (viewer-security §9).
	id := sessionID(r)
	if err := s.store.Verify(r.Context(), h, s.cfg.Hasher); err != nil {
		slog.Info("viewer audit", "action", "object.verify", "session", sessionHandle(id), "hash", h, "valid", false)
		w.Header().Set("HX-Trigger", "object-status-updated")
		outcome := s.describeVerifyFailure(r.Context(), h, err)
		outcome.Integrity = "corrupt"
		outcome.IntegrityLabel = integrityLabel("corrupt")
		s.sessions.setVerification(id, h.String(), "corrupt", outcome)
		outcome.Checked = checkedLabel(s.sessions, id, h.String())
		s.render(w, "result-swap", outcome)
		return
	}
	slog.Info("viewer audit", "action", "object.verify", "session", sessionHandle(id), "hash", h, "valid", true)
	w.Header().Set("HX-Trigger", "object-status-updated")
	outcome := verifiedOutcome(h)
	// The report is stored before the check time is stamped onto it: the label
	// is relative ("3m ago"), so it has to be derived per render rather than
	// frozen at the moment of the check.
	s.sessions.setVerification(id, h.String(), "verified", outcome)
	outcome.Checked = checkedLabel(s.sessions, id, h.String())
	s.render(w, "result-swap", outcome)
}

// verifiedOutcome is the report of a successful check. The sweep and the
// per-object action share it so a swept object and a clicked one describe
// themselves identically.
func verifiedOutcome(h cas.Digest) actionOutcome {
	return actionOutcome{
		OK:             true,
		Headline:       "Verified",
		Summary:        "Stored bytes hash to this address.",
		Expected:       h.String(),
		Integrity:      "verified",
		IntegrityLabel: integrityLabel("verified"),
	}
}

// describeVerifyFailure turns a verification error into operator-facing prose.
func (s *Server) describeVerifyFailure(ctx context.Context, h cas.Digest, err error) actionOutcome {
	switch {
	case errors.Is(err, cas.ErrDigestMismatch):
		return actionOutcome{
			Headline: "Corrupt",
			Summary:  "Stored bytes no longer hash to this address, so the content has changed since it was written. The object is unusable and must be restored from a backup or re-ingested.",
			Expected: h.String(),
			Actual:   s.recomputeDigest(ctx, h),
		}
	case errors.Is(err, cas.ErrNotFound):
		return actionOutcome{
			Headline: "Missing",
			Summary:  "The object is no longer present in the store.",
			Expected: h.String(),
		}
	default:
		return actionOutcome{
			Headline: "Unreadable",
			Summary:  "The object could not be read for verification, so its integrity is unknown.",
			Expected: h.String(),
			Detail:   err.Error(),
		}
	}
}

// recomputeDigest reports the digest the stored bytes actually hash to, so a
// mismatch shows both sides rather than one unexplained number. It returns an
// empty string when the bytes cannot be re-read.
func (s *Server) recomputeDigest(ctx context.Context, h cas.Digest) string {
	rc, err := s.store.Get(ctx, h)
	if err != nil {
		return ""
	}
	defer rc.Close()
	actual, err := s.cfg.Hasher.Digest(rc)
	if err != nil {
		return ""
	}
	return actual.String()
}
