//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// killedBy reports the signal that killed a process, so the diagnosis can name it: a
// SIGKILL is the kernel reclaiming memory, while a step reporting its own failure is a
// different thing entirely. The signal is a process status on a POSIX host; the platform
// without one says so with false.
func killedBy(err error) (int, bool) {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return 0, false
	}
	status, ok := exited.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, false
	}
	return int(status.Signal()), true
}
