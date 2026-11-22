package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/state"
)

// A supervisor start refused because the caller may not supervise the loop
// (another BB thread owns it, a transfer is pending, the loop is
// superseded) used to say so on stderr only, which a background start, a
// service or a script throws away. It is now recorded where a person looks:
// runs.jsonl and a small record that status and doctor show until a
// supervisor starts or a check is recorded. Neither changes control: a
// caller that is refused may not change the loop, only say that it tried.
// The record holds a fixed reason code, never an error's text, so no
// thread ID enters it.

const startRefusalName = "supervisor-start-refused.json"

// startRefusalLogEvery is how often a refusal that repeats with the same
// reason (a service restarting into it) is added to runs.jsonl again.
const startRefusalLogEvery = time.Hour

// Reason codes of a refused supervisor start.
const (
	refusedOwner      = "owned_by_another_thread"
	refusedTransfer   = "transfer_pending"
	refusedSuperseded = "superseded"
	refusedOther      = "not_permitted"
)

var refusalText = map[string]string{
	refusedOwner:      "another BB thread owns the loop",
	refusedTransfer:   "a BB transfer of the loop is pending",
	refusedSuperseded: "the loop is superseded by another",
	refusedOther:      "the loop's ownership or transfer record did not permit it",
}

type startRefusal struct {
	Version int `json:"version"`
	// At is the first refusal with this reason, Last the latest, Count how
	// many there have been since At.
	At    time.Time `json:"at"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
	// Logged is when runs.jsonl last got a line for it.
	Logged time.Time `json:"logged"`
	// Mode is how the supervisor was being started: start, daemon or
	// service.
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	// CallerThread says whether the refused start had a BB_THREAD_ID: set
	// or unset. The value itself is not recorded.
	CallerThread string `json:"callerThread"`
}

func refusalReason(err error) string {
	switch {
	case errors.Is(err, control.ErrOwnerConflict):
		return refusedOwner
	case errors.Is(err, control.ErrTransferPending):
		return refusedTransfer
	case errors.Is(err, control.ErrSuperseded):
		return refusedSuperseded
	}
	return refusedOther
}

// refusedStart records a refused supervisor start, then reports it as
// ownershipError does. A held lock (a check or a daemon running) and a
// cancelled start are not refusals and are not recorded.
func refusedStart(e env, stateDir, mode string, err error) int {
	if !errors.Is(err, lock.ErrHeld) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		noteStartRefused(stateDir, mode, err, time.Now())
	}
	return ownershipError(e, err)
}

func noteStartRefused(stateDir, mode string, cause error, now time.Time) {
	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		return // never create a loop's state directory for a refusal
	}
	caller := "unset"
	if os.Getenv("BB_THREAD_ID") != "" {
		caller = "set"
	}
	now = now.UTC()
	r := startRefusal{Version: 1, At: now, Last: now, Count: 1, Mode: mode, Reason: refusalReason(cause), CallerThread: caller}
	if old := loadStartRefusal(stateDir); old != nil && old.Reason == r.Reason {
		r.At, r.Count, r.Logged = old.At, old.Count+1, old.Logged
	}
	log := r.Logged.IsZero() || now.Sub(r.Logged) >= startRefusalLogEvery
	if log {
		r.Logged = now
	}
	if b, err := json.Marshal(r); err == nil {
		if f, err := os.CreateTemp(stateDir, ".start-refused-*"); err == nil {
			_, werr := f.Write(b)
			if cerr := f.Close(); werr == nil && cerr == nil {
				_ = snapshot.Replace(f.Name(), filepath.Join(stateDir, startRefusalName))
			}
			os.Remove(f.Name())
		}
	}
	if log {
		_ = state.Append(stateDir, state.Event{At: now, Event: "daemon", Reason: "start_refused", Error: r.Reason, Extra: map[string]any{"mode": mode, "callerThread": caller, "count": r.Count}})
	}
}

// clearStartRefused removes the record once a supervisor has started.
func clearStartRefused(stateDir string) {
	_ = snapshot.Remove(filepath.Join(stateDir, startRefusalName))
}

func loadStartRefusal(stateDir string) *startRefusal {
	b, err := os.ReadFile(filepath.Join(stateDir, startRefusalName))
	if err != nil {
		return nil
	}
	var r startRefusal
	if json.Unmarshal(b, &r) != nil || r.Version != 1 || r.At.IsZero() || refusalText[r.Reason] == "" {
		return nil
	}
	if r.Last.Before(r.At) {
		r.Last = r.At
	}
	return &r
}

// currentStartRefusal is the refusal to show, or nil: none is shown while a
// daemon answers, or once a check has been recorded after it, since
// something is supervising the loop then. (The check itself removes it;
// this read writes nothing.)
func currentStartRefusal(stateDir string, checkedAt time.Time, d daemonStatus) *startRefusal {
	r := loadStartRefusal(stateDir)
	switch {
	case r == nil:
		return nil
	case !checkedAt.IsZero() && checkedAt.After(r.Last):
		return nil
	case d.State == "responsive":
		return nil
	}
	return r
}

func (r *startRefusal) sentence() string {
	times := ""
	if r.Count > 1 {
		times = fmt.Sprintf(" (%d times since %s)", r.Count, r.At.Format(time.RFC3339))
	}
	return fmt.Sprintf("the last supervisor start (%s) was refused at %s%s: %s; it ran with BB_THREAD_ID %s. No supervisor has started since", r.Mode, r.Last.Format(time.RFC3339), times, refusalText[r.Reason], r.CallerThread)
}
