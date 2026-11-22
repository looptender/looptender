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

// diskWatch is a daemon's watch on its state directory's volume. An episode
// begins when the volume falls below the filling threshold and ends only
// when it is back above it by a margin (diskRecoveryMargin), so free space
// hovering at a threshold is one episode, not one per crossing. Within an
// episode each level (filling, nearly full) is recorded in runs.jsonl and
// reported through the error event at most once, and no two reports are
// closer than diskReportEvery, except that "nearly full" is told at once;
// the end of an episode is recorded. A
// restarted daemon tells again once.
type diskWatch struct {
	level      string // the episode's worst level so far; ok outside one
	told       map[string]bool
	lastReport time.Time
	now        func() time.Time
}

// diskReportEvery is the least time between two disk space reports from one
// daemon.
const diskReportEvery = time.Hour

// diskRecoveryMargin is how far above a threshold free space must be before
// the volume counts as recovered from it: the larger of 256 MiB and 10% of
// the threshold.
func diskRecoveryMargin(threshold uint64) uint64 {
	if m := threshold / 10; m > 256<<20 {
		return m
	}
	return 256 << 20
}

// diskLevelWithMargin is the reading's level, except that leaving a worse
// level needs the margin above its threshold.
func diskLevelWithMargin(r diskspace.Reading, current string) string {
	level := r.Level()
	switch {
	case current == diskspace.Critical && level != diskspace.Critical && r.Available < diskspace.FloorBytes+diskRecoveryMargin(diskspace.FloorBytes):
		return diskspace.Critical
	case (current == diskspace.Critical || current == diskspace.Low) && level == diskspace.OK && r.Available < r.LowAt()+diskRecoveryMargin(r.LowAt()):
		return diskspace.Low
	}
	return level
}

func (w *diskWatch) check(e env, cfg *config.Config) {
	if w == nil {
		return
	}
	now := time.Now
	if w.now != nil {
		now = w.now
	}
	if w.level == "" {
		w.level = diskspace.OK
	}
	r := diskspace.Of(cfg.Loop.StateDir)
	if !r.Known {
		return // an unknown reading neither starts nor ends an episode
	}
	level := diskLevelWithMargin(r, w.level)
	rank := map[string]int{diskspace.OK: 0, diskspace.Low: 1, diskspace.Critical: 2}
	switch {
	case level == diskspace.OK && w.level != diskspace.OK:
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "daemon", Reason: "disk_ok",
			Extra: map[string]any{"availableBytes": r.Available, "totalBytes": r.Total}})
		w.level, w.told = diskspace.OK, nil
	case rank[level] > rank[w.level] || (level != diskspace.OK && !w.told[level] && rank[level] >= rank[w.level]):
		if rank[level] > rank[w.level] {
			w.level = level
		}
		// "Nearly full" is told at once, even within the hour: it is the one
		// that cannot wait, and it is told at most once an episode anyway.
		soon := !w.lastReport.IsZero() && now().Sub(w.lastReport) < diskReportEvery && level != diskspace.Critical
		if w.told[level] || soon {
			return // told already, or too soon after the last report
		}
		if w.told == nil {
			w.told = map[string]bool{}
		}
		w.told[level], w.lastReport = true, now()
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "daemon", Reason: "disk_" + level, Error: r.Sentence(),
			Extra: map[string]any{"availableBytes": r.Available, "totalBytes": r.Total}})
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		defer cancel()
		event := report.Event{Type: "error", Loop: cfg.Loop.Name, At: time.Now().UTC(), Text: cfg.Loop.Name + ": " + r.Sentence() + ".",
			Data: map[string]any{"cause": "disk_space_" + level, "availableBytes": r.Available, "totalBytes": r.Total}}
		if err := reporter(e, cfg).Report(ctx, event); err != nil {
			e.errf("disk space report attempt failed; it will not be repeated for this episode")
		}
	}
}
