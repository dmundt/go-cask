//go:build !windows

package main

import (
	"bytes"
	"os/exec"
)

// fakeExitError builds the error a command killed by a signal reports: it runs one command
// that kills itself with that signal, so the process's own status carries the kill rather
// than a hand-written value. The signal is named the way `kill` takes it. A signal is a POSIX
// process status; on Windows the same helper reports that this host cannot describe a kill.
func fakeExitError(signal string) error {
	command := exec.Command("sh", "-c", "kill -"+signal+" $$")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	exited, ok := err.(*exec.ExitError)
	if !ok {
		return nil
	}
	exited.Stderr = stderr.Bytes()
	return exited
}
