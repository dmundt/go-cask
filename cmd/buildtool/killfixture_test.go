//go:build !windows

package main

import (
	"os/exec"
	"strconv"
)

// fakeExitError builds the error a command killed by a signal reports, so the kill path is
// driven as a toolchain actually reports it — an ExitError whose status carries a signal —
// rather than as a hand-written error. A signal is a POSIX process status; on Windows the
// same helper reports that this host cannot describe a kill at all.
func fakeExitError(signal int) error {
	command := exec.Command("sh", "-c", "kill -"+strconv.Itoa(-signal)+" $$")
	_ = command.Run()
	if command.ProcessState == nil {
		return nil
	}
	return command.ProcessState.Err()
}
