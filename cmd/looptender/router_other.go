//go:build !unix

package main

import "context"

func routerCommand(e env, _ []string) int {
	e.errf("router: not supported on this system yet")
	return exitFailed
}

func readRouterSummary(_ context.Context, _ string, _ bool) (routerSummary, bool) {
	return routerSummary{}, false
}
