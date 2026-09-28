//go:build windows

package main

// signalOf names the signal that killed a process, and reports whether this platform can
// tell at all.
//
// Windows cannot: its process status carries no signal, so a terminated process reports an
// ordinary status and a killed step there must not be diagnosed as a signal kill.
func signalOf(status int) (string, bool) {
	return "", false
}
