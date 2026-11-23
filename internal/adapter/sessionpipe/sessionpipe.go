// Package sessionpipe observes one agent session that sessionpipe reports. It
// reads a tier-0 events file the person told sessionpipe to write (a file
// sink) and nothing else: no sessionpipe command runs during a check, its
// outbox is never read, and nothing is sent to the session.
//
// The target is observed, never woken. Send and Stop return
// adapter.ErrUnsupported, and the decision engine never asks for either on
// this kind of target (config.ObservedOnly).
//
// Only an allowlist of event types and data keys is used, and each value
// must be one of the words the protocol defines. Everything else in a line,
// the session's title, folder, branch and any text, is decoded into nothing
// and never stored, logged or shown. A line above tier 0 makes the whole
// file unreadable for this adapter until it is replaced: a sink at a higher
// tier carries words and paths, and looptender does not read them.
package sessionpipe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/evidence"
	"github.com/looptender/looptender/internal/snapshot"
)

// Limits on one check's read.
const (
	// MaxReadPerCheck is the most of the events file one check reads. A
	// longer backlog is read over several checks, and the state is unknown
	// until the reader has caught up.
	MaxReadPerCheck = 4 << 20
	// MaxLine is the longest line used. A tier-0 event is a few hundred
	// bytes; a longer line is skipped unread.
	MaxLine = 64 << 10
	// maxAttentions bounds the open attention ids kept.
	maxAttentions = 16
	// prefixLen is how much of the file's start identifies it, so a file
	// that was replaced or truncated is read again from its start.
	prefixLen = 256
	// ReadStateFile is the reader's record in the target's state directory.
	ReadStateFile = "sessionpipe-read.json"
	// EvidenceSource names this adapter's observations.
	EvidenceSource = "sessionpipe_events"
)

// ErrNotTierZero is returned while the events file holds a line that is not
// a tier-0 protocol-1 event. Nothing past that line is read.
var (
	// ErrNotRegular: the events file is a FIFO, device, directory or other
	// non-regular file, after following a link. It is never opened.
	ErrNotRegular = errors.New("the sessionpipe events file is not a regular file; point sessionpipe.events_file at the file sink's file")
	// ErrWritableByOthers: a user other than the owner can write the file,
	// and so could make the session look waiting, ended or idle.
	ErrWritableByOthers = errors.New("other users can write the sessionpipe events file, so it is not read; remove group and other write access (chmod go-w)")
)

var ErrNotTierZero = errors.New("the sessionpipe events file holds an event above tier 0 or of another protocol; looptender reads only a tier-0 file sink (sessionpipe sink add file:PATH --tier 0); replace the file")

// Adapter observes one session.
type Adapter struct {
	harness, id string
	file        string
	staleAfter  time.Duration
	dir         string
	now         func() time.Time
}

// New returns the adapter for c. dir is the target's state directory.
func New(c config.Sessionpipe, dir string) *Adapter {
	harness, id, _ := config.SplitSessionpipeRef(c.Session)
	stale := c.StaleAfter.Duration
	if stale <= 0 {
		stale = config.DefaultSessionpipeStaleAfter
	}
	return &Adapter{harness: harness, id: id, file: c.EventsFile, staleAfter: stale, dir: dir, now: time.Now}
}

// CheckAt fixes the clock, for tests and for a check's single notion of now.
func (a *Adapter) CheckAt(t time.Time) { a.now = func() time.Time { return t } }

// Name identifies the adapter kind.
func (a *Adapter) Name() string { return config.SessionpipeAdapter }

// ConversationKey is the session as sessionpipe names it.
func (a *Adapter) ConversationKey() (string, bool) { return a.harness + ":" + a.id, a.id != "" }

// Send is never possible: the target is observed, never woken.
func (a *Adapter) Send(context.Context, string, adapter.Mode) error {
	return adapter.ErrUnsupported{Op: "sending to an observed sessionpipe session"}
}

// Stop is never possible: looptender does not stop a session it observes.
func (a *Adapter) Stop(context.Context) error {
	return adapter.ErrUnsupported{Op: "stopping an observed sessionpipe session"}
}

// readState is what the reader keeps between checks. It holds no text from
// the events file: only offsets, a hash of the file's start and words from
// the protocol's fixed vocabulary.
type readState struct {
	Version int `json:"version"`
	// Offset is where the next read starts; Prefix the hash of the file's
	// first PrefixLen bytes when it was last read.
	Offset int64 `json:"offset"`
	// Size and FileID are the file's size and identity (device and inode,
	// or volume and file index) when it was last read. A smaller size or
	// another identity means it was replaced.
	Size      int64  `json:"size,omitempty"`
	FileID    string `json:"fileId,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	PrefixLen int    `json:"prefixLen,omitempty"`
	// Skipping: the read stopped inside a line longer than MaxLine, which is
	// skipped up to its end.
	Skipping bool `json:"skipping,omitempty"`
	// Seen: an event of the session has been read.
	Seen    bool  `json:"seen,omitempty"`
	HasSeq  bool  `json:"hasSeq,omitempty"`
	LastSeq int64 `json:"lastSeq,omitempty"`
	// LastAt is the newest applied event's time.
	LastAt time.Time `json:"lastAt,omitzero"`
	// Phase is idle, active, error or ended.
	Phase      string `json:"phase,omitempty"`
	Background int    `json:"background,omitempty"`
	// Attention and TurnID are tokens (idToken) of the protocol's ids, never
	// the ids a writer chose.
	Attention []string `json:"attention,omitempty"`
	TurnID    string   `json:"turnId,omitempty"`
	// Skipped counts lines that were not valid JSON or too long.
	Skipped int `json:"skipped,omitempty"`
}

const readStateVersion = 2

func (a *Adapter) statePath() string { return filepath.Join(a.dir, ReadStateFile) }

func (a *Adapter) load() readState {
	b, err := os.ReadFile(a.statePath())
	var s readState
	if err != nil || json.Unmarshal(b, &s) != nil || s.Version != readStateVersion || s.Offset < 0 {
		return readState{Version: readStateVersion}
	}
	return s
}

func (a *Adapter) save(s readState) error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return snapshot.WriteFileAtomic(a.statePath(), b, 0o600, false)
}

func unknown() decide.Observation {
	return decide.Observation{State: decide.Unknown, ErrorClass: decide.ErrNone, Evidence: evidence.Detail{Source: EvidenceSource, Category: "unavailable", Code: "unavailable"}}
}

// Observe reads what the events file gained since the last check and reports
// the session's state. A session that has said nothing for stale_after is
// unknown, never idle.
func (a *Adapter) Observe(ctx context.Context) (decide.Observation, error) {
	if err := ctx.Err(); err != nil {
		return unknown(), err
	}
	s := a.load()
	// Stat first, following a link: only a regular file is opened, so a
	// FIFO or device in its place is refused rather than waited on.
	before, err := os.Stat(a.file)
	if err != nil {
		return unknown(), fmt.Errorf("the sessionpipe events file cannot be read: %w", pathless(err))
	}
	if !before.Mode().IsRegular() {
		return unknown(), ErrNotRegular
	}
	if refused, known := othersCanWrite(a.file, before); known && refused {
		return unknown(), ErrWritableByOthers
	}
	f, err := openEvents(a.file)
	if err != nil {
		return unknown(), fmt.Errorf("the sessionpipe events file cannot be read: %w", pathless(err))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return unknown(), fmt.Errorf("the sessionpipe events file cannot be read: %w", pathless(err))
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return unknown(), errors.New("the sessionpipe events file changed while it was opened; the next check reads it again")
	}
	size, id := info.Size(), fileIdentity(f, info)
	replaced := s.Offset > size || size < s.Size || (s.FileID != "" && id != "" && id != s.FileID) || !a.samePrefix(f, s)
	if replaced {
		// Replaced, truncated or rotated: read it again from its start.
		s = readState{Version: readStateVersion}
	}
	caughtUp, rerr := a.read(f, size, &s)
	if rerr != nil {
		// Fail closed, and keep the offset so it stays failed until the file
		// is replaced.
		return unknown(), rerr
	}
	if err := a.remember(f, size, &s); err != nil {
		return unknown(), err
	}
	s.Size, s.FileID = size, id
	if err := a.save(s); err != nil {
		return unknown(), fmt.Errorf("the sessionpipe reader's record cannot be saved: %w", pathless(err))
	}
	return a.observation(s, caughtUp), nil
}

// samePrefix reports whether the file still starts as it did.
func (a *Adapter) samePrefix(f *os.File, s readState) bool {
	if s.PrefixLen == 0 {
		return true
	}
	b := make([]byte, s.PrefixLen)
	if _, err := f.ReadAt(b, 0); err != nil {
		return false
	}
	return hash(b) == s.Prefix
}

// remember records the hash of the file's start, growing it to prefixLen.
func (a *Adapter) remember(f *os.File, size int64, s *readState) error {
	n := int64(prefixLen)
	if size < n {
		n = size
	}
	if int(n) == s.PrefixLen {
		return nil
	}
	b := make([]byte, n)
	if _, err := f.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("the sessionpipe events file cannot be read: %w", pathless(err))
	}
	s.Prefix, s.PrefixLen = hash(b), int(n)
	return nil
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// read applies the complete lines between s.Offset and at most
// MaxReadPerCheck further. caughtUp: no complete line is left unread.
func (a *Adapter) read(f *os.File, size int64, s *readState) (caughtUp bool, err error) {
	want := size - s.Offset
	if want <= 0 {
		return true, nil
	}
	// Whatever a read to the end leaves is at most one incomplete line.
	caughtUp = true
	if want > MaxReadPerCheck {
		want, caughtUp = MaxReadPerCheck, false
	}
	buf := make([]byte, want)
	n, rerr := f.ReadAt(buf, s.Offset)
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return false, fmt.Errorf("the sessionpipe events file cannot be read: %w", pathless(rerr))
	}
	buf = buf[:n]
	pos := 0
	for pos < len(buf) {
		nl := bytes.IndexByte(buf[pos:], '\n')
		if nl < 0 {
			rest := len(buf) - pos
			if s.Skipping || rest > MaxLine {
				// Inside a line that is too long: skip what was read of it.
				s.Skipping = true
				s.Offset += int64(rest)
				pos = len(buf)
			}
			break
		}
		line := buf[pos : pos+nl]
		pos += nl + 1
		if s.Skipping {
			s.Skipping = false
			s.Skipped++
		} else if len(line) > MaxLine {
			s.Skipped++
		} else if len(bytes.TrimSpace(line)) > 0 {
			if err := a.apply(line, s); err != nil {
				return false, err
			}
		}
		s.Offset += int64(nl + 1)
	}
	return caughtUp && n == int(want), nil
}

// event is everything this adapter takes from a line. Fields not named here,
// the session's title, folder, repository, branch, model and account, and
// every other data key, are never decoded.
type event struct {
	Protocol *int   `json:"protocol"`
	Type     string `json:"type"`
	Time     string `json:"time"`
	Tier     *int   `json:"tier"`
	Harness  struct {
		Name string `json:"name"`
	} `json:"harness"`
	Session struct {
		ID  string `json:"id"`
		Seq *int64 `json:"seq"`
	} `json:"session"`
	Data struct {
		Reason      string `json:"reason"`
		Kind        string `json:"kind"`
		AttentionID string `json:"attention_id"`
		Background  *int   `json:"background"`
		TurnID      string `json:"turn_id"`
	} `json:"data"`
}

// idWord accepts an id from the protocol's character set only, so no free
// text is ever kept in its place.
func idWord(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}

// idToken stands for an id from the file: a short digest, so nothing a
// writer of the file chose is kept or returned as it was written.
func idToken(id string) string {
	h := sha256.Sum256([]byte("sessionpipe-id\x00" + id))
	return hex.EncodeToString(h[:8])
}

func (a *Adapter) apply(line []byte, s *readState) error {
	var e event
	if json.Unmarshal(line, &e) != nil {
		s.Skipped++
		return nil
	}
	if e.Tier == nil || *e.Tier != 0 || e.Protocol == nil || *e.Protocol != 1 {
		return ErrNotTierZero
	}
	if e.Harness.Name != a.harness || e.Session.ID != a.id {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		return nil
	}
	if now := a.now(); at.After(now.Add(5 * time.Minute)) {
		at = now // a clock far ahead does not keep the session fresh
	}
	if e.Session.Seq != nil {
		if s.HasSeq && *e.Session.Seq <= s.LastSeq {
			return nil // already applied, or older than what was
		}
	}
	if !a.applyType(e, s) {
		return nil
	}
	if e.Session.Seq != nil {
		s.HasSeq, s.LastSeq = true, *e.Session.Seq
	}
	s.Seen = true
	if at.After(s.LastAt) {
		s.LastAt = at
	}
	return nil
}

// applyType changes the state for one allowlisted event; false when the type
// or a value is not one the protocol defines at tier 0.
func (a *Adapter) applyType(e event, s *readState) bool {
	d := e.Data
	switch e.Type {
	case "session.started":
		s.Phase, s.Background, s.Attention, s.TurnID = "idle", 0, nil, ""
	case "session.heartbeat":
		if s.Phase != "ended" && s.Phase != "error" {
			s.Phase = "active"
		}
	case "turn.started":
		s.Phase, s.Background, s.Attention, s.TurnID = "active", 0, nil, ""
		if idWord(d.TurnID) {
			s.TurnID = idToken(d.TurnID)
		}
	case "turn.ended":
		switch d.Reason {
		case "stop", "interrupt":
			s.Phase = "idle"
		case "error":
			s.Phase = "error"
		default:
			return false
		}
		s.Attention, s.Background = nil, 0
		if d.Background != nil && *d.Background > 0 {
			s.Background = min(*d.Background, 1000)
		}
		if idWord(d.TurnID) {
			s.TurnID = idToken(d.TurnID)
		}
	case "attention.needed":
		switch d.Kind {
		case "permission", "question", "elicitation":
			if !idWord(d.AttentionID) {
				return false
			}
			tok := idToken(d.AttentionID)
			for _, id := range s.Attention {
				if id == tok {
					return true
				}
			}
			if len(s.Attention) >= maxAttentions {
				s.Attention = s.Attention[1:]
			}
			s.Attention = append(s.Attention, tok)
		case "idle":
			s.Phase = "idle"
		case "error":
			s.Phase = "error"
		default:
			return false
		}
	case "attention.cleared":
		if !idWord(d.AttentionID) {
			return false
		}
		tok := idToken(d.AttentionID)
		kept := s.Attention[:0]
		for _, id := range s.Attention {
			if id != tok {
				kept = append(kept, id)
			}
		}
		s.Attention = kept
		if len(s.Attention) == 0 {
			s.Attention = nil
		}
	case "session.ended", "session.forgotten":
		s.Phase, s.Attention, s.Background = "ended", nil, 0
	default:
		return false
	}
	return true
}

func (a *Adapter) observation(s readState, caughtUp bool) decide.Observation {
	o := unknown()
	if !caughtUp || !s.Seen {
		return o
	}
	if s.Phase == "ended" {
		o.State, o.Gone = decide.Idle, true
		return o
	}
	if a.now().Sub(s.LastAt) > a.staleAfter {
		return o // silence is not idle
	}
	o.Background = s.Background
	switch {
	case len(s.Attention) > 0:
		o.State, o.AwaitingInput = decide.Active, true
	case s.Phase == "active":
		o.State = decide.Active
	case s.Phase == "idle":
		o.State = decide.Idle
	case s.Phase == "error":
		// Tier 0 says that a turn ended in error, not why.
		o.State, o.ErrorClass, o.ErrorTurn = decide.Error, decide.ErrUnknown, s.TurnID
	}
	return o
}

// pathless drops the path from a file error, so no error text carries it.
func pathless(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
