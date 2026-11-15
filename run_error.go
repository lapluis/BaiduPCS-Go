package main

import (
	"os/exec"
	"syscall"
)

// Do not implement cli.ExitCoder: the CLI library would terminate an active
// interactive session before its caller can display the error and resume.
type systemCommandError struct {
	cause error
}

func (e *systemCommandError) Error() string { return e.cause.Error() }
func (e *systemCommandError) Unwrap() error { return e.cause }

func (e *systemCommandError) exitStatus() int {
	processError, ok := e.cause.(*exec.ExitError)
	if !ok {
		return 1
	}
	if code := processError.ExitCode(); code > 0 {
		return code
	}
	if status, ok := processError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}
