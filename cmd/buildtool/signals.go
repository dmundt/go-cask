package main

import (
	"errors"
	"os/exec"
)

// exitCodeOf returns the exit code a failed command reported, or a negative number for the
// signal that killed it. A failure that is not a process exit — a command that could not be
// started at all — is 0, which is deliberately "no signal": a step that never ran was not
// killed, and a diagnosis must not invent one.
func exitCodeOf(err error) int {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return 0
	}
	return exited.ProcessState.ExitCode()
}
