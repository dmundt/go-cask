//go:build windows

package main

// fakeExitError reports that this platform cannot describe a signal kill: a Windows process
// status carries no signal, so there is no status bit to build and the kill path has
// nothing to exercise there.
func fakeExitError(signal int) error {
	return nil
}
