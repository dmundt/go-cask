// Package atomicfile publishes a file atomically: the bytes go to a temp file
// in the destination's own directory, that file is fsynced, and a rename puts
// it in place. A reader therefore sees either the previous file or the complete
// new one, and a crash never leaves a half-written destination.
//
// It is internal to cas, and it exists so that the three paths that publish a
// file this way share one implementation (go-cask#339): the filesystem Backend
// (cas/backend/fs, its object Put), the pack manifest writer (cas/pack,
// SaveWith) and the reference store (cas/refs, its ref files). Each hand-rolled
// the sequence, with three temp-name rules and two durability rules between
// them — cas/pack never fsynced the destination directory, so a manifest rename
// could be lost by a crash the other two survive. The durability decision is
// still the caller's and still explicit (Options.SyncDir); cas/pack now takes
// it, because one publish means one durability rule.
//
// The temp name is the destination path plus ".tmp", then ".tmp.<n>" while that
// name is taken (createTemp). An existing temp name is never removed to make
// room: a stale leftover from a crashed writer and a temp file a *concurrent*
// writer is still filling look identical from here, and replacing the second
// would destroy that writer's bytes — the rename it is about to do would then
// fail with ENOENT. The leftovers are instead named by one predicate,
// IsTempFile, which cas/backend/fs (List, Clean) and cas/refs (List) skip.
package atomicfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/dmundt/go-cask/cas/backend"
)

// Options are the caller's publish decisions.
type Options struct {
	// Mode is the permission the destination gets. The temp file is created
	// with exactly these bits (the process umask applies, as it does to any
	// create), and the rename carries them to the destination. Zero means
	// 0o644.
	Mode os.FileMode
	// SyncDir asks for a best-effort fsync of the destination's parent
	// directory after the rename, so the rename itself survives a crash and
	// not only the file content. It is the one documented durability
	// difference between callers: a content-addressed object and a ref are
	// published with it, a pack manifest is too. It is a no-op on Windows,
	// which cannot open a directory for Sync.
	SyncDir bool
	// ExistingIsSuccess reports a rename that failed because the destination
	// already holds a regular file as a publish that succeeded. It is the
	// content-addressed store's idempotent Put (cas-core §4.4): the bytes
	// under a digest are already the bytes the caller wanted, so an existing
	// regular file satisfies the write. Anything else at the destination (a
	// directory, a device) stays a failure. The temp file is removed either
	// way.
	ExistingIsSuccess bool
}

// Phase names the step of Publish that failed, so a caller can report it in its
// own operator-facing vocabulary ("object", "manifest", "ref") without
// re-implementing the sequence.
type Phase uint8

const (
	// PhaseDir is the destination's parent directory.
	PhaseDir Phase = iota + 1
	// PhaseTemp is the temp file create.
	PhaseTemp
	// PhaseWrite is the copy of the caller's bytes into the temp file.
	PhaseWrite
	// PhaseSync is the temp file's fsync.
	PhaseSync
	// PhaseClose is the temp file's close.
	PhaseClose
	// PhasePublish is the rename that publishes the temp file.
	PhasePublish
	// PhaseDirSync is the parent directory's fsync (Options.SyncDir).
	PhaseDirSync
)

// String names the phase for an error message: "create temp file", "publish",
// "sync directory". A caller that wraps the error once therefore reports both
// the step and its own context ("pack: write manifest /x/meta.json: create temp
// file: open …"), and the shared publish keeps one vocabulary for the steps.
func (p Phase) String() string {
	switch p {
	case PhaseDir:
		return "create directory"
	case PhaseTemp:
		return "create temp file"
	case PhaseWrite:
		return "write temp file"
	case PhaseSync:
		return "sync temp file"
	case PhaseClose:
		return "close temp file"
	case PhasePublish:
		return "publish"
	case PhaseDirSync:
		return "sync directory"
	default:
		return "publish"
	}
}

// Error is a failed phase of Publish: which phase stopped and what the
// filesystem returned. The text names the phase and then the underlying error,
// so a caller that wraps this once reports both without a per-phase switch; the
// underlying error stays reachable through Unwrap for errors.Is and errors.As.
type Error struct {
	// Phase is the step Publish stopped at.
	Phase Phase
	// Err is the error the filesystem returned.
	Err error
}

// Error names the phase and the underlying failure.
func (e *Error) Error() string { return e.Phase.String() + ": " + e.Err.Error() }

// Unwrap returns the underlying filesystem error.
func (e *Error) Unwrap() error { return e.Err }

// FailedPhase reports which phase of Publish stopped and what it returned. ok is
// false when err did not come from Publish (a canceled context, which Publish
// returns as ctx.Err(), or an error a caller's own seam produced), in which case
// cause is err unchanged. A caller that wants the step named without unwrapping
// can simply wrap err: Error's text carries Phase.String().
func FailedPhase(err error) (phase Phase, cause error, ok bool) {
	var pubErr *Error
	if errors.As(err, &pubErr) {
		return pubErr.Phase, pubErr.Err, true
	}
	return 0, err, false
}

// Publish writes r to path atomically: it creates path's parent directory, fills
// a temp file beside the destination, fsyncs and closes it, renames it over the
// destination and, when asked, fsyncs the parent directory. Every failure
// removes the temp file, so a failed publish leaves no scratch behind and the
// previous destination untouched.
//
// Publish honors ctx before it creates anything, before it creates the temp file
// and before the rename that makes the file visible, so a canceled context
// publishes nothing. The write itself runs through backend.ContextReader, which
// checks ctx on every read.
func Publish(ctx context.Context, path string, r io.Reader, opts Options) error {
	return publishWith(ctx, path, r, opts, defaultOps())
}

// SyncParentDir fsyncs the directory containing path, so a rename into it
// survives a crash and not only the renamed file's content. A no-op on Windows,
// which cannot open a directory for Sync; every other failure is reported.
func SyncParentDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// IsTempFile reports whether name is a temp file this package's publish may
// leave behind: "<name>.tmp" or a collision fallback "<name>.tmp.<n>". It is the
// one predicate the callers that list a directory use to skip scratch files.
func IsTempFile(name string) bool {
	_, after, ok := strings.Cut(name, ".tmp")
	if !ok {
		return false
	}
	rest := after
	if rest == "" {
		return true
	}
	if rest[0] != '.' {
		return false
	}
	_, err := strconv.Atoi(rest[1:])
	return err == nil
}

// file is the write side of the temp file the publish uses: the surface it
// needs, so a test can make any single step fail. An *os.File satisfies it.
type file interface {
	Name() string
	Write(p []byte) (int, error)
	Sync() error
	Close() error
}

// ops is the filesystem seam of the publish. Production always uses
// defaultOps; a test injects a failure per phase, so the error paths are
// covered without asking a real filesystem to fail on demand (the seam
// cas/pack carried before this package owned the sequence, go-cask#256).
type ops struct {
	mkdirAll   func(path string, mode os.FileMode) error
	createTemp func(path string, mode os.FileMode) (file, string, error)
	remove     func(name string) error
	rename     func(oldpath, newpath string) error
	syncDir    func(path string) error
}

// defaultOps returns the real filesystem operations.
func defaultOps() ops {
	return ops{
		mkdirAll:   os.MkdirAll,
		createTemp: createTemp,
		remove:     os.Remove,
		rename:     os.Rename,
		syncDir:    SyncParentDir,
	}
}

// publishWith is Publish over an injectable filesystem, so every phase's failure
// is testable.
func publishWith(ctx context.Context, path string, r io.Reader, opts Options, o ops) error {
	mode := opts.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &Error{Phase: PhaseDir, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, tmp, err := o.createTemp(path, mode)
	if err != nil {
		return &Error{Phase: PhaseTemp, Err: err}
	}
	discard := func() {
		// Best effort: the write already failed, so a cleanup error here
		// cannot be reported without hiding the primary error.
		_ = f.Close()
		_ = o.remove(tmp)
	}
	if _, err := io.Copy(f, backend.ContextReader{Ctx: ctx, R: r}); err != nil {
		discard()
		return &Error{Phase: PhaseWrite, Err: err}
	}
	if err := f.Sync(); err != nil {
		discard()
		return &Error{Phase: PhaseSync, Err: err}
	}
	if err := f.Close(); err != nil {
		_ = o.remove(tmp) // the temp object is unusable
		return &Error{Phase: PhaseClose, Err: err}
	}
	if err := ctx.Err(); err != nil {
		_ = o.remove(tmp) // a canceled publish must not become visible
		return err
	}
	if err := o.rename(tmp, path); err != nil {
		if opts.ExistingIsSuccess {
			if fi, statErr := os.Stat(path); statErr == nil && fi.Mode().IsRegular() {
				_ = o.remove(tmp) // the destination already holds these bytes
				return nil
			}
		}
		_ = o.remove(tmp)
		return &Error{Phase: PhasePublish, Err: err}
	}
	if opts.SyncDir {
		if err := o.syncDir(path); err != nil {
			return &Error{Phase: PhaseDirSync, Err: err}
		}
	}
	return nil
}

// createTemp creates a uniquely named temp file for path: "<path>.tmp", then
// "<path>.tmp.<n>" while that name is taken. It opens with O_CREATE|O_EXCL, so
// two writers never share a temp file, and it never removes an existing one (see
// the package doc). mode is the permission the destination will keep.
func createTemp(path string, mode os.FileMode) (file, string, error) {
	base := path + ".tmp"
	for i := range 10000 {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("temp name exhausted for %s", path)
}
