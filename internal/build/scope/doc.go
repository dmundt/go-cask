// Package scope owns what a change set triggers: its classification, and the gate
// run's scope, package concurrency and escape hatches.
//
// changes.go classifies the changed paths; verify.go decides what a run covers.
package scope
