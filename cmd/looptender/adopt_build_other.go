//go:build !unix

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// adoptSupported: on Windows a process cannot replace its own image while
// keeping its identity and the daemon lock, so a daemon keeps the build it
// started with; restarting supervision uses the installed one.
const adoptSupported = false

var execImage = func(string, []string, []string) error {
	return errors.New("not available on this platform")
}

func adoptFileRefusal(*os.File) string { return "not available on this platform" }
func adoptDirsRefusal([]string) string { return "not available on this platform" }
func keepFDAcrossExec(*os.File) (uintptr, error) {
	return 0, errors.New("not available on this platform")
}

func adoptCommand(ctx context.Context, c *adoptCandidate, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.path, args...)
}

func execAdoptCandidate(*adoptCandidate, []string, []string, []*os.File) error {
	return errors.New("not available on this platform")
}

func adoptRunnable() string { return "not available on this platform" }
