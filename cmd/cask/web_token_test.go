package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHandler captures every record the code under test logs, whatever its
// level: the contract for the viewer's startup token is that NO log level emits
// it, so a guard has to listen at the lowest level (viewer-security §9, §11).
// It is safe for concurrent use because runWeb logs from its serving goroutine.
type recordingHandler struct {
	mu   sync.Mutex
	text strings.Builder
}

// Enabled implements slog.Handler at every level.
func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler, recording the level, message, and attributes.
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text.WriteString(r.Level.String())
	h.text.WriteString(" ")
	h.text.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		h.text.WriteString(" ")
		h.text.WriteString(a.Key)
		h.text.WriteString("=")
		h.text.WriteString(a.Value.String())
		return true
	})
	h.text.WriteString("\n")
	return nil
}

// WithAttrs implements slog.Handler.
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// String returns everything recorded so far.
func (h *recordingHandler) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

// installRecorder makes a recording handler the process default logger for the
// duration of the test. The default logger is process-wide, so the previous one
// is restored by cleanup; the cmd/cask tests run sequentially, so no other test
// observes the swap.
func installRecorder(t *testing.T) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestAnnounceLoginNeverLogsToken is the regression guard for issue #179: the
// startup token may reach the operator's terminal, but no slog handler may ever
// see it, at any level. It fails if any log call in the announcement carries
// the token — the code before this fix did exactly that with
// slog.Warn("viewer startup token", "admin_token", token).
func TestAnnounceLoginNeverLogsToken(t *testing.T) {
	const token = "AAAA-BBBB-CCCC"
	for _, tc := range []struct {
		name        string
		generated   bool
		interactive bool
		wantShown   bool
	}{
		{"generated, interactive terminal", true, true, true},
		{"generated, no terminal", true, false, false},
		{"supplied, interactive terminal", false, true, false},
		{"supplied, no terminal", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := installRecorder(t)
			var out strings.Builder
			announceLogin(&out, "http://127.0.0.1:8080", token, tc.generated, tc.interactive)
			if logged := logs.String(); strings.Contains(logged, token) {
				t.Fatalf("the process log contains the startup token:\n%s", logged)
			}
			if shown := strings.Contains(out.String(), token); shown != tc.wantShown {
				t.Fatalf("token shown = %v, want %v (output %q)", shown, tc.wantShown, out.String())
			}
		})
	}
}

// TestResolveStartupTokenSources covers the unattended token sources of issue
// #179: a token read from -token-file or CASK_VIEWER_TOKEN is used as given and
// marked not generated (so it is never displayed), the flag wins over the
// environment, and a missing or empty file fails without echoing a token.
func TestResolveStartupTokenSources(t *testing.T) {
	const (
		fromFile = "FEED-FACE-0001"
		fromEnv  = "FEED-FACE-0002"
	)
	file := filepath.Join(t.TempDir(), "startup-token")
	if err := os.WriteFile(file, []byte(fromFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty-token")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "absent")

	t.Run("token file", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		logs := installRecorder(t)
		token, generated, err := resolveStartupToken(webArgs{tokenFile: file})
		if err != nil || token != fromFile || generated {
			t.Fatalf("resolveStartupToken(-token-file) = (%q, %v, %v), want (%q, false, nil)", token, generated, err, fromFile)
		}
		if logged := logs.String(); strings.Contains(logged, fromFile) {
			t.Fatalf("token resolution logged the token:\n%s", logged)
		}
	})

	t.Run("flag wins over the environment", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, fromEnv)
		token, generated, err := resolveStartupToken(webArgs{tokenFile: file})
		if err != nil || token != fromFile || generated {
			t.Fatalf("resolveStartupToken(-token-file, env) = (%q, %v, %v), want the file's token", token, generated, err)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "  "+fromEnv+"  ")
		logs := installRecorder(t)
		token, generated, err := resolveStartupToken(webArgs{})
		if err != nil || token != fromEnv || generated {
			t.Fatalf("resolveStartupToken(env) = (%q, %v, %v), want (%q, false, nil)", token, generated, err, fromEnv)
		}
		if logged := logs.String(); strings.Contains(logged, fromEnv) {
			t.Fatalf("token resolution logged the token:\n%s", logged)
		}
	})

	t.Run("generated when nothing is supplied", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		token, generated, err := resolveStartupToken(webArgs{})
		if err != nil || !generated {
			t.Fatalf("resolveStartupToken() = (%q, %v, %v), want a generated token", token, generated, err)
		}
		if len(token) != 14 {
			t.Fatalf("generated token %q is not the documented 3-group form", token)
		}
	})

	t.Run("unusable file is an error naming the file", func(t *testing.T) {
		t.Setenv(viewerTokenEnv, "")
		for _, path := range []string{missing, empty} {
			token, generated, err := resolveStartupToken(webArgs{tokenFile: path})
			if err == nil || token != "" || generated {
				t.Fatalf("resolveStartupToken(%q) = (%q, %v, %v), want an error", path, token, generated, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not name the file %q", err, path)
			}
		}
	})
}

// TestRunWebNeverLogsSuppliedToken runs the real `cask web` startup path with an
// operator-supplied token and asserts the token reaches neither the process log
// (at any level) nor the announcement: an unattended deployment supplies its
// token instead of having it printed.
func TestRunWebNeverLogsSuppliedToken(t *testing.T) {
	const supplied = "DEAD-BEEF-1234"
	tokenFile := filepath.Join(t.TempDir(), "startup-token")
	if err := os.WriteFile(tokenFile, []byte(supplied), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := installRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(150 * time.Millisecond)
		cancel()
	}()
	if code := runWeb(ctx, modeFlags{store: t.TempDir()}, []string{
		"-bind", "127.0.0.1:0", "-no-open", "-token-file", tokenFile,
	}); code != 0 {
		t.Fatalf("runWeb exit = %d, want 0", code)
	}
	if logged := logs.String(); strings.Contains(logged, supplied) {
		t.Fatalf("the process log contains the supplied startup token:\n%s", logged)
	}
}
