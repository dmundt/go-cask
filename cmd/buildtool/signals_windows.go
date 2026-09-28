//go:build windows

package main

import (
	"errors"
	"os/exec"
)

// killedBy reports the signal that killed a process, and false on a platform whose process
// status cannot carry one. Windows cannot: a terminated process there reports an ordinary
// exit status, so a kill and a failure are indistinguishable and neither is reported as a
// signal. The check is still made, so a warning that cannot name a kill is not confused with
// one that never looked.
func killedBy(err error) (int, bool) {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return 0, false
	}
	return 0, false
}
