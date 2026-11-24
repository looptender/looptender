//go:build !darwin && !linux && !windows

package sessionpipe

// procStart cannot be read here; a recorded wake then counts as pending only
// while its process ID is alive, up to askStale.
func procStart(int) string { return "" }
