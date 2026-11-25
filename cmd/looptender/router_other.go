//go:build !unix

package main

func routerCommand(e env, _ []string) int {
	e.errf("router: not supported on this system yet")
	return exitFailed
}
