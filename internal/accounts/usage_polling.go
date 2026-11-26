package accounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
)

// Usage polling is a separate, opt-in record. Old catalog readers ignore it;
// catalog/quota reads never create it or authorize provider access.
const UsagePollingName = "usage-polling.json"

type UsagePolling struct {
	SchemaVersion int         `json:"schemaVersion"`
	Entries       []UsagePoll `json:"entries"`
}
type UsagePoll struct {
	ID         string     `json:"id"`
	Revision   string     `json:"revision"`
	Binding    string     `json:"binding"`
	Executable string     `json:"executableStamp"` // digest of reviewed executable metadata and directory identities, never paths
	Grant      string     `json:"grant"`
	NextAt     time.Time  `json:"nextAttemptAt"`
	AttemptAt  *time.Time `json:"lastAttemptAt,omitempty"`
	Failures   int        `json:"failures"`
}

func validUsagePolling(f UsagePolling) bool {
	if f.SchemaVersion != 1 || f.Entries == nil || len(f.Entries) > MaxEntries {
		return false
	}
	seen := map[string]bool{}
	for _, p := range f.Entries {
		if !validToken(p.ID) || !validToken(p.Revision) || !validDigest(p.Binding) || !validDigest(p.Executable) || !validToken(p.Grant) || seen[p.ID] || p.NextAt.IsZero() || p.Failures < 0 || p.Failures > 10 || (p.AttemptAt != nil && p.AttemptAt.IsZero()) {
			return false
		}
		seen[p.ID] = true
	}
	return true
}
func LoadUsagePolling(dir string) (UsagePolling, error) {
	empty := UsagePolling{SchemaVersion: 1, Entries: []UsagePoll{}}
	if checkDir(dir) != nil {
		return empty, ErrInvalid
	}
	path := filepath.Join(dir, UsagePollingName)
	st, err := snapshot.LstatRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil || !config.PrivateFile(st) || st.Mode()&os.ModeSymlink != 0 || st.Size() > MaxBytes {
		return empty, ErrInvalid
	}
	f, err := snapshot.OpenRecord(path)
	if err != nil {
		return empty, ErrInvalid
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !config.PrivateFile(st) || st.Size() > MaxBytes {
		return empty, ErrInvalid
	}
	st, err = snapshot.LstatRecord(path)
	if err != nil || !config.PrivateFile(st) || st.Mode()&os.ModeSymlink != 0 {
		return empty, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return empty, ErrInvalid
	}
	var out UsagePolling
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if !QuotaJSONUnique(raw) || !quotaCanonicalFields(raw, reflect.TypeOf(out)) || d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || !validUsagePolling(out) {
		return empty, ErrInvalid
	}
	return out, nil
}
func UpdateUsagePolling(dir string, fn func(*UsagePolling) error) error {
	if checkDir(dir) != nil || checkFile(filepath.Join(dir, "usage-polling.lock")) != nil {
		return ErrInvalid
	}
	held, err := lock.AcquireNamed(dir, "usage-polling")
	if err != nil {
		return err
	}
	defer held.Release()
	f, err := LoadUsagePolling(dir)
	if err != nil {
		return err
	}
	if err = fn(&f); err != nil {
		return err
	}
	if !validUsagePolling(f) {
		return ErrInvalid
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return ErrInvalid
	}
	return control.WritePrivateFile(filepath.Join(dir, UsagePollingName), append(raw, '\n'))
}

// UsagePollGuard serializes automatic batches across processes. The existing
// CheckGuard independently excludes manual quota/login checks per selection.
func UsagePollGuard(dir string) (*lock.Lock, error) {
	if checkDir(dir) != nil || checkFile(filepath.Join(dir, "usage-poll-run.lock")) != nil {
		return nil, ErrInvalid
	}
	return lock.AcquireNamed(dir, "usage-poll-run")
}
