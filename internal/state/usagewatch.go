package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	// so it is warned once a window: a reading whose reset is within
	// SameWindow of it is the same window, however the reported reset
	// drifts between reads.
	WarnedFor time.Time `json:"warnedFor,omitempty"`
}

// LoadUsageWatch reads the record; nil, nil when there is none.
func LoadUsageWatch(stateDir string) (*UsageWatch, error) {
	var u UsageWatch
	found, err := loadSmallRecord(stateDir, UsageWatchFileName, &u)
	if err != nil || !found {
		return nil, err
	}
	return &u, nil
}

// SaveUsageWatch replaces the record.
func SaveUsageWatch(stateDir string, u UsageWatch) error {
	return saveSmallRecord(stateDir, UsageWatchFileName, u)
}

// UsageWatchSettingName holds the usage watch's setting (looptender
// usage-watch). It is kept in the state directory, not in the loop's
// configuration: a build without the watch never reads it, so turning the
// watch on never stops an older build from loading the configuration.
const UsageWatchSettingName = "usage-watch-setting.json"

// UsageWatchSetting turns the watch on for a loop: the accounts catalog
// entry its own conversation uses, how often its quota is read, and how
// close the run-out must be for the one warning.
type UsageWatchSetting struct {
	Account    string    `json:"account"`
	Every      string    `json:"every"`
	WarnWithin string    `json:"warnWithin"`
	SetAt      time.Time `json:"setAt"`
}

var usageWatchAccount = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Durations are the setting's two durations; ok is false when either is
// unreadable or outside its bounds (every 10m to 24h, warn within 1h to
// 168h), or the account is not a catalog ID's shape.
func (s UsageWatchSetting) Durations() (every, warnWithin time.Duration, ok bool) {
	every, err1 := time.ParseDuration(s.Every)
	warnWithin, err2 := time.ParseDuration(s.WarnWithin)
	ok = err1 == nil && err2 == nil && usageWatchAccount.MatchString(s.Account) &&
		every >= 10*time.Minute && every <= 24*time.Hour && warnWithin >= time.Hour && warnWithin <= 168*time.Hour
	return every, warnWithin, ok
}

// LoadUsageWatchSetting reads the setting; nil, nil when the watch is off.
func LoadUsageWatchSetting(stateDir string) (*UsageWatchSetting, error) {
	var s UsageWatchSetting
	found, err := loadSmallRecord(stateDir, UsageWatchSettingName, &s)
	if err != nil || !found {
		return nil, err
	}
	if _, _, ok := s.Durations(); !ok {
		return nil, fmt.Errorf("%s: not a valid setting", UsageWatchSettingName)
	}
	return &s, nil
}

// SaveUsageWatchSetting replaces the setting, or removes it (off) when s is
// nil.
func SaveUsageWatchSetting(stateDir string, s *UsageWatchSetting) error {
	if s == nil {
		if err := os.Remove(filepath.Join(stateDir, UsageWatchSettingName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, _, ok := s.Durations(); !ok {
		return fmt.Errorf("%s: not a valid setting", UsageWatchSettingName)
	}
	return saveSmallRecord(stateDir, UsageWatchSettingName, s)
}

func loadSmallRecord(stateDir, name string, v any) (bool, error) {
	r, err := snapshot.OpenRecord(filepath.Join(stateDir, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, err := io.ReadAll(io.LimitReader(r, maxUsageWatchBytes+1))
	r.Close()
	if err != nil {
		return false, err
	}
	if len(b) > maxUsageWatchBytes {
		return false, fmt.Errorf("%s: too large", name)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return true, nil
}

func saveSmallRecord(stateDir, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
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
	return snapshot.Replace(tmpName, filepath.Join(stateDir, name))
}

// SameWindow is how far two readings' reset times may differ and still be
// one window: providers report the reset to the second, and it drifts.
const SameWindow = time.Hour

// Warned says whether the agent was warned about the window resetting at
// resetsAt.
func (u UsageWatch) Warned(resetsAt time.Time) bool {
	if u.WarnedFor.IsZero() {
		return false
	}
	d := resetsAt.Sub(u.WarnedFor)
	return d < SameWindow && d > -SameWindow
}
