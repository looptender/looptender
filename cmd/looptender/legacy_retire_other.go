//go:build !darwin && !linux

package main

func legacyRetireCommand(e env, _ []string) int {
	e.errf("legacy-retire is unsupported on this platform; no action taken")
	return exitFailed
}
