//go:build unix && !darwin && !linux

package main

import "context"

type routerServiceHost struct{}

func localRouterServiceHost() routerServiceHost { return routerServiceHost{} }

func routerServiceCommand(e env, _ []string, _ routerServiceHost) int {
	e.errf("router service: user-service management currently supports macOS launchd and Linux systemd user services")
	return exitFailed
}

func routerServiceRegistrationSummary(_ context.Context, _ string, _ bool, _ routerServiceHost) string {
	return "unavailable"
}
