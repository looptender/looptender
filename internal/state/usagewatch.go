package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/snapshot"
)

// UsageWatchFileName is the usage-burn watch's record (config.UsageWatch).
// It is its own record, not a field of state.json, so it changes no state
// format: a build without it neither reads nor rewrites it.
const UsageWatchFileName = "usage-watch.json"

const maxUsageWatchBytes = 16 << 10

// UsageWatch is the last quota read of the account a loop's conversation
// uses, its weekly projection, and the window its agent was warned for.
type UsageWatch struct {
	Account  string `json:"account"`
	Provider string `json:"provider,omitempty"`
	// CheckedAt is the last read attempt; Reason says why it gave no
	// reading, and is empty when it did.
	CheckedAt time.Time `json:"checkedAt"`
	Reason    string    `json:"reason,omitempty"`
	// ReadAt and the fields after it are the last reading that gave the
	// weekly window, and its projection.
	ReadAt         time.Time `json:"readAt,omitempty"`
	Bucket         string    `json:"bucket,omitempty"`
	UsedPercent    float64   `json:"usedPercent,omitempty"`
	ResetsAt       time.Time `json:"resetsAt,omitempty"`
	PercentPerHour float64   `json:"percentPerHour,omitempty"`
	// ExhaustsAt is set only when, at that rate, the window runs out before
	// ResetsAt.
	ExhaustsAt time.Time `json:"exhaustsAt,omitempty"`
	// WarnedFor is the ResetsAt of the window the agent was warned about,
	// so it is warned once a window.
	WarnedFor time.Time `json:"warnedFor,omitempty"`
}

// LoadUsageWatch reads the record; nil, nil when there is none.
func LoadUsageWatch(stateDir string) (*UsageWatch, error) {
	r, err := snapshot.OpenRecord(filepath.Join(stateDir, UsageWatchFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(r, maxUsageWatchBytes+1))
	r.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > maxUsageWatchBytes {
		return nil, fmt.Errorf("%s: too large", UsageWatchFileName)
	}
	var u UsageWatch
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("%s: %w", UsageWatchFileName, err)
	}
	return &u, nil
}

// SaveUsageWatch replaces the record.
func SaveUsageWatch(stateDir string, u UsageWatch) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, "."+UsageWatchFileName+"-*")
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
	return snapshot.Replace(name, filepath.Join(stateDir, UsageWatchFileName))
}
