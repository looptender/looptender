package main

import (
	"context"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/diskspace"
	"github.com/looptender/looptender/internal/report"
	"github.com/looptender/looptender/internal/state"
)

// diskSpaceStatus is the room left on the volume that holds the loop's
// state directory, for status and doctor.
type diskSpaceStatus struct {
	// Level is ok, low (below the larger of 1 GiB and 2% of the volume),
	// critical (below 100 MiB) or unknown.
	Level          string `json:"level"`
	AvailableBytes uint64 `json:"availableBytes,omitempty"`
	TotalBytes     uint64 `json:"totalBytes,omitempty"`
	Message        string `json:"message,omitempty"`
}

func describeDiskSpace(dir string) diskSpaceStatus {
	r := diskspace.Of(dir)
	return diskSpaceStatus{Level: r.Level(), AvailableBytes: r.Available, TotalBytes: r.Total, Message: r.Sentence()}
}

// doctorDiskSpace adds state.disk_space: a warning when the volume is
// filling, an error when it is nearly full.
func doctorDiskSpace(r *doctorReport, dir string) {
	s := describeDiskSpace(dir)
	switch s.Level {
	case diskspace.OK:
		r.add("state.disk_space", "ok", "The volume that holds the state directory has "+diskspace.Size(s.AvailableBytes)+" free of "+diskspace.Size(s.TotalBytes)+".", "")
	case diskspace.Low:
		r.add("state.disk_space", "warning", capitalize(s.Message)+".", "Free space on the volume: below 100 MiB, records may fail to save.")
	case diskspace.Critical:
		r.add("state.disk_space", "error", capitalize(s.Message)+".", "Free space on the volume now. Nothing is sent without its record saved first, but holds, history and reports need room to be saved.")
	default:
		r.add("state.disk_space", "unavailable", capitalize(s.Message)+".", "")
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// diskWatch is a daemon's watch on its state directory's volume: each time
// the volume gets worse (filling, then nearly full) it is recorded once in
// runs.jsonl and reported once through the error event; its recovery is
// recorded. A restarted daemon tells again once.
type diskWatch struct {
	level string
}

func (w *diskWatch) check(e env, cfg *config.Config) {
	if w == nil {
		return
	}
	r := diskspace.Of(cfg.Loop.StateDir)
	level := r.Level()
	worse := func(a, b string) bool { // b is worse than a
		rank := map[string]int{diskspace.OK: 0, diskspace.Unknown: 0, diskspace.Low: 1, diskspace.Critical: 2}
		return rank[b] > rank[a]
	}
	prev := w.level
	if prev == "" {
		prev = diskspace.OK
	}
	w.level = level
	switch {
	case worse(prev, level):
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "daemon", Reason: "disk_" + level, Error: r.Sentence(),
			Extra: map[string]any{"availableBytes": r.Available, "totalBytes": r.Total}})
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		defer cancel()
		event := report.Event{Type: "error", Loop: cfg.Loop.Name, At: time.Now().UTC(), Text: cfg.Loop.Name + ": " + r.Sentence() + ".",
			Data: map[string]any{"cause": "disk_space_" + level, "availableBytes": r.Available, "totalBytes": r.Total}}
		if err := reporter(e, cfg).Report(ctx, event); err != nil {
			e.errf("disk space report attempt failed; it will not be repeated for this episode")
		}
	case level == diskspace.OK && (prev == diskspace.Low || prev == diskspace.Critical):
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "daemon", Reason: "disk_ok",
			Extra: map[string]any{"availableBytes": r.Available, "totalBytes": r.Total}})
	}
}
