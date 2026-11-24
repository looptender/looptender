//go:build !unix && !windows

package sessionpipe

import "os/exec"

func detach(*exec.Cmd) {}

// alive cannot be told here; a recorded ask counts as finished, so a wake is
// never refused for good on a platform that cannot check.
func alive(int) bool { return false }
