//go:build !windows

package main

import "syscall"

// signalOf names the signal a terminated process reported, and reports whether this
// platform carries one in a process's status. Only POSIX hosts do: elsewhere the status is
// an ordinary exit code and a kill and an ordinary failure are indistinguishable.
//
// The name is what the diagnosis prints, because that is the fact a lane can act on: a
// SIGKILL is the kernel reclaiming memory, while a failure with no signal is the step
// reporting its own verdict.
func signalOf(status int) (string, bool) {
	if status >= 0 {
		return "", false
	}
	switch syscall.Signal(-status) {
	case syscall.SIGKILL:
		return "SIGKILL", true
	case syscall.SIGTERM:
		return "SIGTERM", true
	case syscall.SIGINT:
		return "SIGINT", true
	case syscall.SIGQUIT:
		return "SIGQUIT", true
	case syscall.SIGABRT:
		return "SIGABRT", true
	case syscall.SIGSEGV:
		return "SIGSEGV", true
	case syscall.SIGHUP:
		return "SIGHUP", true
	}
	return "", false
}
