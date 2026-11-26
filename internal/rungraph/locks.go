package rungraph

import "github.com/looptender/looptender/internal/lock"

// InspectLocks returns ephemeral diagnostics only. They are never persisted in
// the graph or linked to a run: the lock format supplies no such owner binding.
func InspectLocks(dir string) []lock.Inspection {
	return []lock.Inspection{lock.Inspect(dir, lock.Tick), lock.Inspect(dir, lock.Daemon)}
}
