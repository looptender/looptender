//go:build unix && !linux

package main

import (
	"context"
	"os"
	"os/exec"
)

// macOS and the BSDs cannot run an open descriptor: the file is run by its
// path, after the daemon has checked, immediately before, that the path
// still leads to the checked file with the checked contents. A file
// swapped in between that last check and the exec is the remaining window;
// the directory checks keep other users out of it.

func adoptCommand(ctx context.Context, c *adoptCandidate, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.path, args...)
}

func execAdoptCandidate(c *adoptCandidate, argv, env []string, _ []*os.File) error {
	return execImage(c.path, argv, env)
}

// adoptRunnable says why a checked file cannot be run here, or "".
func adoptRunnable() string { return "" }
