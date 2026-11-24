//go:build !unix

package main

// The account router's processes need a private channel that exists only on
// Unix systems so far.
func routerRunCommand(e env, _ []string) int {
	e.errf("router: not supported on this system yet")
	return exitFailed
}

func routerFrontCommand(e env, _ []string) int {
	e.errf("front: not supported on this system yet")
	return exitFailed
}
