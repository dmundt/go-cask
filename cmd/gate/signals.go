package main

import (
	"strconv"
	"syscall"
)

// killedStep reports whether a step's command was killed by a signal rather than failing on
// its own merits, and which one. It is deliberately quiet on a host whose process status
// carries no signal: there a kill and an ordinary failure are indistinguishable, and a guess
// would misdirect the lane that owns the commit.
func killedStep(err error) (string, bool) {
	signal, killed := killedBy(err)
	if !killed {
		return "", false
	}
	return signalName(syscall.Signal(signal)), true
}

// signalName names a signal the way a report reads it. A signal this table does not name is
// still named by its number, because that is the difference between a red gate a lane can
// read and one it cannot.
func signalName(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	return "signal " + strconv.Itoa(int(signal))
}
