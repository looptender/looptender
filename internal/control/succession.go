package control

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/snapshot"
)

// SuccessionFileName is the successor's record of the loop it continues:
// continued-from.json in the successor's state directory. Like archive.json
// it is a file of its own, which builds from before it neither read nor
// rewrite. The predecessor is archived by the successor's first completed
// check after the record was made, if the predecessor is held then; until
// that check, and while the predecessor still supervises, the record waits,
// and neither loop is hidden.
const SuccessionFileName = "continued-from.json"

const successionFileLimit = 8 << 10

// LoopRef names a loop of a line of work. RegistryID is its registration,
// when it has one; lists name a loop by that only.
type LoopRef struct {
	Loop       string    `json:"loop"`
	Config     string    `json:"config,omitempty"`
	StateDir   string    `json:"stateDir,omitempty"`
	Thread     string    `json:"thread,omitempty"`
	RegistryID string    `json:"registryId,omitempty"`
	At         time.Time `json:"at"`
}

// Succession is the record. Source is declared (archive --succeeded-by,
// init --succeeds) or supersede (transfer supersede named the successor's
// thread). By is "person", or the BB thread that declared it.
type Succession struct {
	Version    int       `json:"version"`
	From       LoopRef   `json:"from"`
	Source     string    `json:"source"`
	By         string    `json:"by"`
	DeclaredAt time.Time `json:"declaredAt"`
	// FromOwner and FromGeneration are the predecessor's owner when the
	// succession was declared (empty for a loop with none). The predecessor
	// is archived only while they are unchanged. FromHeld says it was held
	// then, and FromControlAt is its control record's updatedAt: a held
	// predecessor whose control record changed since (it was resumed, or
	// held again) is not archived, and the succession is cancelled.
	FromOwner      string     `json:"fromOwner,omitempty"`
	FromGeneration uint64     `json:"fromGeneration,omitempty"`
	FromHeld       bool       `json:"fromHeld,omitempty"`
	FromControlAt  time.Time  `json:"fromControlAt"`
	ArchivedAt     *time.Time `json:"archivedAt,omitempty"`
	// CancelledAt and Cancelled: the succession ended without archiving,
	// and why. A new declaration is needed to archive the predecessor.
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`
	Cancelled   string     `json:"cancelled,omitempty"`
	// Waiting is why the predecessor is not archived yet, after a check
	// that tried; empty before the first try and once it is archived.
	Waiting string `json:"waiting,omitempty"`
}

// ErrSuccessionUnreadable is a record that exists and cannot be read.
var ErrSuccessionUnreadable = errors.New("continued-from.json could not be read")

// LoadSuccession reads the record: nil with no error when there is none.
func LoadSuccession(stateDir string) (*Succession, error) {
	reader, err := snapshot.OpenRecord(filepath.Join(stateDir, SuccessionFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrSuccessionUnreadable
	}
	defer reader.Close()
	b, err := io.ReadAll(io.LimitReader(reader, successionFileLimit+1))
	var s Succession
	if err != nil || len(b) > successionFileLimit || json.Unmarshal(b, &s) != nil || s.Version != 1 || s.From.Loop == "" || s.From.StateDir == "" || s.DeclaredAt.IsZero() {
		return nil, ErrSuccessionUnreadable
	}
	return &s, nil
}

// SaveSuccession replaces the record.
func SaveSuccession(stateDir string, s Succession) error {
	s.Version = 1
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(stateDir, SuccessionFileName), append(b, '\n'), 0o600)
}

// SucceededByFileName is the predecessor's own mark of a declared
// succession: succeeded-by.json in its state directory, naming the successor
// and the declaration. The successor's record alone, in a directory its own
// agent may write, is not enough to archive another loop: completion
// requires this mark to match, read under the predecessor's control lock.
const SucceededByFileName = "succeeded-by.json"

// SucceededBy is the mark.
type SucceededBy struct {
	Version    int       `json:"version"`
	StateDir   string    `json:"stateDir"`
	Loop       string    `json:"loop"`
	DeclaredAt time.Time `json:"declaredAt"`
}

func SaveSucceededBy(stateDir string, m SucceededBy) error {
	m.Version = 1
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(stateDir, SucceededByFileName), append(b, '\n'), 0o600)
}

// LoadSucceededBy reads the mark: nil when there is none or it is unreadable.
func LoadSucceededBy(stateDir string) *SucceededBy {
	reader, err := snapshot.OpenRecord(filepath.Join(stateDir, SucceededByFileName))
	if err != nil {
		return nil
	}
	defer reader.Close()
	b, err := io.ReadAll(io.LimitReader(reader, successionFileLimit+1))
	var m SucceededBy
	if err != nil || len(b) > successionFileLimit || json.Unmarshal(b, &m) != nil || m.Version != 1 || m.StateDir == "" {
		return nil
	}
	return &m
}
