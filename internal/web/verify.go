// On-demand integrity checks: one object, or the whole store, with the finding
// recorded in the session so the inspector can replay it.

package web

import (
	"context"
	"errors"
	"fmt"
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
	verified, corrupt, unchecked := 0, 0, 0
	for _, h := range digests {
		if err := r.Context().Err(); err != nil {
			// The sweep stopped part-way, so its result is incomplete and the
			// entries already recorded no longer describe the store. Answering
			// before returning keeps a canceled request from reading as an
			// empty 200 with a control that never reported anything.
			http.Error(w, "request canceled", http.StatusServiceUnavailable)
			return
		}
		actual, err := s.verifyObject(r.Context(), h)
		if err != nil {
			state := integrityOf(err)
			outcome := s.describeVerifyFailure(h, err)
			outcome.Actual = actual
			outcome.Integrity = state
			outcome.IntegrityLabel = integrityLabel(state)
			s.sessions.setVerification(id, h.String(), state, outcome)
			if state == "corrupt" {
				corrupt++
			} else {
				unchecked++
				// The rendered row carries the owned "Unreadable" prose; the
				// audit line carries the cause the operator would otherwise
				// have read out of the response (viewer-design §3).
				slog.Info("viewer audit", "action", "object.verify-unreadable", "session", sessionHandle(id), "hash", h, "err", err)
			}
			continue
		}
		s.sessions.setVerification(id, h.String(), "verified", verifiedOutcome(h))
		verified++
	}
	slog.Info("viewer audit", "action", "object.verify-all", "session", sessionHandle(id), "objects", len(digests), "verified", verified, "corrupt", corrupt, "not-verified", unchecked)
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
	// CSRF is the per-session CSRF token.
	CSRF string
	// Label is the visible control text.
	Label string
}

// actionOutcome is the structured result of an object action (verification).
// It replaces the raw error string: a sentinel classifies the failure and the
// recomputed digest shows the operator exactly how the bytes diverged. Every
// field is owned prose the viewer wrote; a backend error never reaches it, so
// nothing the store's implementation puts in an error — a path, a syscall, a
// library name — can be rendered to the operator (viewer-design §3). The error
// itself goes to the audit line, which is where an operator who needs the cause
// reads it.
type actionOutcome struct {
	// OK reports whether the action succeeded.
	OK bool
	// Headline is the concise result title.
	Headline string
	// Summary is the human-readable result summary.
	Summary string
	// Expected is the expected digest.
	Expected string
	// Actual is the digest computed from stored bytes.
	Actual string
	// Checked is the formatted verification time.
	Checked string
	// Integrity and IntegrityLabel refresh the inspector's Integrity row out
	// of band; they stay empty for actions that leave no object behind.
	// Integrity is the refreshed integrity state.
	Integrity string
	// IntegrityLabel is the human-readable refreshed integrity state.
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
	actual, err := s.verifyObject(r.Context(), h)
	if err != nil {
		outcome := s.describeVerifyFailure(h, err)
		state := integrityOf(err)
		outcome.Actual = actual
		outcome.Integrity = state
		outcome.IntegrityLabel = integrityLabel(state)
		s.sessions.setVerification(id, h.String(), state, outcome)
		outcome.Checked = checkedLabel(s.sessions, id, h.String())
		// The error is recorded here instead of in the rendered result: the
		// response carries owned prose, the audit line carries the cause
		// (viewer-design §3).
		slog.Info("viewer audit", "action", "object.verify", "session", sessionHandle(id), "hash", h, "valid", false, "state", state, "err", err)
		w.Header().Set("HX-Trigger", "object-status-updated")
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

// integrityOf classifies a verification failure the way cas.VerifyAll does:
// only a digest mismatch means the stored bytes are corrupt. An object that is
// absent or cannot be read was never verified, so it keeps the neutral
// "not-verified" state while describeVerifyFailure's prose still says why
// ("Missing", "Unreadable") — otherwise a file deleted out of band would be
// reported as corrupted content, and the sweep's counts would disagree with
// `cask verify --all` on the same store.
func integrityOf(err error) string {
	if errors.Is(err, cas.ErrDigestMismatch) {
		return "corrupt"
	}
	return "not-verified"
}

// describeVerifyFailure turns a verification error into operator-facing prose.
// An error the sentinels do not classify is reported as "Unreadable" with the
// viewer's own sentence: the error text stays out of the result, because an
// interpreter error carries whatever the failing layer put in it — an absolute
// path, a syscall name — and the operator needs the finding, not the layer
// (viewer-design §3). The audit line carries the error.
func (s *Server) describeVerifyFailure(h cas.Digest, err error) actionOutcome {
	switch {
	case errors.Is(err, cas.ErrDigestMismatch):
		return actionOutcome{
			Headline: "Corrupt",
			Summary:  "Stored bytes no longer hash to this address, so the content has changed since it was written. The object is unusable and must be restored from a backup or re-ingested.",
			Expected: h.String(),
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
			Summary:  "The object could not be read for verification, so its integrity is unknown. The viewer log records why the read failed.",
			Expected: h.String(),
		}
	}
}

// verifyObject hashes one open stream and returns its actual digest. Keeping
// the digest from the verification pass avoids a second full read when a
// mismatch must be explained to an operator. With no readable digest to report
// it returns the empty string, and the error says why.
func (s *Server) verifyObject(ctx context.Context, h cas.Digest) (string, error) {
	if err := s.cfg.Hasher.Validate(h); err != nil {
		return "", err
	}
	rc, err := s.store.Get(ctx, h)
	if err != nil {
		return "", err
	}
	actual, err := s.cfg.Hasher.Digest(rc)
	if err != nil {
		_ = rc.Close() // the read error is the one worth reporting
		return "", fmt.Errorf("cas: verify read: %w", err)
	}
	if err := rc.Close(); err != nil {
		return "", fmt.Errorf("cas: verify close: %w", err)
	}
	if !actual.Equal(h) {
		return actual.String(), fmt.Errorf("%w: %s", cas.ErrDigestMismatch, h)
	}
	return actual.String(), nil
}
