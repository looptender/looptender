//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
)

// On Linux the checked file itself is run, through its open descriptor, so
// a file swapped in at its path after the check is never what runs.

func adoptCommand(ctx context.Context, c *adoptCandidate, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.Args[0] = c.path
	cmd.ExtraFiles = []*os.File{c.file}
	return cmd
}

func execAdoptCandidate(c *adoptCandidate, argv, env []string, _ []*os.File) error {
	return execImage("/proc/self/fd/"+strconv.FormatUint(uint64(c.file.Fd()), 10), argv, env)
}

// adoptRunnable says why a checked file cannot be run here, or "".
func adoptRunnable() string {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		return "/proc is unavailable, so the checked file cannot be run through its descriptor; restart supervision to use the installed build"
	}
	return ""
}
