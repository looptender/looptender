package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/state"
)

// A supervisor start refused because the caller may not supervise the loop
// (another BB thread owns it, a transfer is pending, the loop is
// superseded) used to say so on stderr only, which a background start, a
// service or a script throws away. It is now recorded where a person looks:
// runs.jsonl and a small record that status and doctor show until a
// supervisor starts. Neither changes control: a caller that is refused may
// not change the loop, only say that it tried.

const startRefusalName = "supervisor-start-refused.json"

type startRefusal struct {
	Version int       `json:"version"`
	At      time.Time `json:"at"`
	// Mode is how the supervisor was being started: start, daemon or
	// service.
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	// CallerThread says whether the refused start had a BB_THREAD_ID: set
	// or unset. The value itself is not recorded.
	CallerThread string `json:"callerThread"`
}

// refusedStart records a refused supervisor start, then reports it as
// ownershipError does. A held lock (a check or a daemon running) is not a
// refusal and is not recorded.
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
	r := startRefusal{Version: 1, At: now.UTC(), Mode: mode, Reason: cause.Error(), CallerThread: caller}
	if b, err := json.Marshal(r); err == nil {
		if f, err := os.CreateTemp(stateDir, ".start-refused-*"); err == nil {
			_, werr := f.Write(b)
			if cerr := f.Close(); werr == nil && cerr == nil {
				_ = snapshot.Replace(f.Name(), filepath.Join(stateDir, startRefusalName))
			}
			os.Remove(f.Name())
		}
	}
	_ = state.Append(stateDir, state.Event{At: r.At, Event: "daemon", Reason: "start_refused", Error: r.Reason, Extra: map[string]any{"mode": mode, "callerThread": caller}})
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
	if json.Unmarshal(b, &r) != nil || r.Version != 1 || r.At.IsZero() {
		return nil
	}
	return &r
}

func (r *startRefusal) sentence() string {
	return fmt.Sprintf("the last supervisor start (%s) was refused at %s: %s; it ran with BB_THREAD_ID %s. Nothing is supervising the loop because of that start", r.Mode, r.At.Format(time.RFC3339), r.Reason, r.CallerThread)
}
