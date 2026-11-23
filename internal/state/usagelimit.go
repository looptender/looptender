package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/snapshot"
)

// UsageLimitFileName holds the provider usage limit the last check that
// observed the active target found on it. It is its own record, not a field
// of state.json: a build that does not know it neither reads nor rewrites
// it, so adding it changes no state format, and a rollback to such a build
// leaves it in place (stale until a newer build's next check replaces or
// removes it). Nothing decides on it: it is what status and the panel say.
const UsageLimitFileName = "usage-limit.json"

const maxUsageLimitBytes = 16 << 10

// UsageLimit is a provider usage limit observed on a target.
type UsageLimit struct {
	Target   string             `json:"target"`
	Provider string             `json:"provider,omitempty"`
	Window   decide.LimitWindow `json:"window,omitempty"`
	ResetsAt time.Time          `json:"resetsAt"`
	// ObservedAt is the check that found it.
	ObservedAt time.Time `json:"observedAt"`
	// ConfigDir is the CLAUDE_CONFIG_DIR the check ran under, when it was
	// set: for a supervisor started from a BB thread, the directory BB runs
	// its Claude threads with, so the login that reached the limit. Empty
	// when unknown.
	ConfigDir string `json:"configDir,omitempty"`
}

// LoadUsageLimit reads the record; nil, nil when there is none.
func LoadUsageLimit(stateDir string) (*UsageLimit, error) {
	r, err := snapshot.OpenRecord(filepath.Join(stateDir, UsageLimitFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(r, maxUsageLimitBytes+1))
	r.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > maxUsageLimitBytes {
		return nil, fmt.Errorf("%s: too large", UsageLimitFileName)
	}
	var u UsageLimit
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("%s: %w", UsageLimitFileName, err)
	}
	return &u, nil
}

// SaveUsageLimit replaces the record, or removes it when u is nil.
func SaveUsageLimit(stateDir string, u *UsageLimit) error {
	path := filepath.Join(stateDir, UsageLimitFileName)
	if u == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, "."+UsageLimitFileName+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := snapshot.Write(tmp, append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return snapshot.Replace(name, path)
}
