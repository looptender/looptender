//go:build unix && !darwin && !linux

package main

type routerServiceHost struct{}

func localRouterServiceHost() routerServiceHost { return routerServiceHost{} }

func routerServiceCommand(e env, _ []string, _ routerServiceHost) int {
	e.errf("router service: user-service management currently supports macOS launchd and Linux systemd user services")
	return exitFailed
}
