package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/lock"
)

const accountUsageSchedulerInterval = 15 * time.Second

// Only long-lived, non-offline panel/dock processes start this worker. Report
// collection (including native panel list) never executes provider IO. Each
// process is a standby; one catalog-wide OS lock elects the scheduler owner.
// The existing batch/per-selection guards and durable reservation remain a
// second fence across process shutdown, crash and manual reads.
func accountUsageScheduler(ctx context.Context, dir string, env []string, now func() time.Time, run accountQuotaRun, ticks <-chan time.Time) {
	if ticks == nil {
		timer := time.NewTicker(accountUsageSchedulerInterval)
		defer timer.Stop()
		ticks = timer.C
	}
	var held *lock.Lock
	var identity os.FileInfo
	lockPath := filepath.Join(dir, "usage-scheduler.lock")
	defer func() {
		if held != nil {
			// Clear only while still owning the original lock, before releasing it.
			if cur, err := os.Stat(lockPath); err == nil && os.SameFile(identity, cur) {
				_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.SchedulerAt = nil; return nil })
			}
			held.Release()
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		f, err := accounts.LoadUsagePolling(dir)
		if err == nil && len(f.Entries) > 0 {
			if held == nil {
				held, err = accounts.UsageSchedulerGuard(dir)
				if err == nil {
					identity, err = os.Stat(lockPath)
				}
				if err != nil && held != nil {
					held.Release()
					held = nil
				}
			}
			if held != nil {
				cur, err := os.Stat(lockPath)
				if err != nil || !os.SameFile(identity, cur) {
					return
				}
				at := now().UTC()
				err = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.SchedulerAt = &at; return nil })
				if err == nil {
					accountUsagePollDue(ctx, dir, env, now, run)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func startAccountUsageScheduler(ctx context.Context, directory string, offline bool) func() {
	return startAccountUsageSchedulerWith(ctx, directory, offline, time.Now, accountQuotaRead, nil)
}

func startAccountUsageSchedulerWith(ctx context.Context, directory string, offline bool, now func() time.Time, run accountQuotaRun, ticks <-chan time.Time) func() {
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	dir, err := accountsDirectory(directory)
	if offline || err != nil {
		close(done)
	} else {
		go func() {
			defer close(done)
			accountUsageScheduler(child, dir, os.Environ(), now, run, ticks)
		}()
	}
	return func() { cancel(); <-done }
}

// Projection performs file reads only. SchedulerAt is a recent owner heartbeat,
// not a successful provider timestamp, and is never presented as one.
func attachAccountUsageUpdating(view *accounts.View, dir string, env []string, at time.Time) {
	settings, err := accounts.LoadUsagePolling(dir)
	for i := range view.Cards {
		c := &view.Cards[i]
		status := &accounts.UsageUpdating{State: "off", Text: "Keep usage updated: off"}
		c.UsageUpdating = status
		if err != nil {
			status.State = "unavailable"
			status.Text = "Usage update settings unavailable"
			continue
		}
		for _, p := range settings.Entries {
			if p.ID != c.ID {
				continue
			}
			status.State = "paused"
			status.Text = "Usage updates paused: selection changed; review and enable again"
			selection, err := accountQuotaCapture(dir, p.ID, env)
			if err != nil || !accountUsageMatches(p, selection) {
				break
			}
			status.NextAt = &p.NextAt
			status.State = "waiting"
			status.Text = "Usage updates paused: keep a non-offline panel or dock process running"
			if settings.SchedulerAt != nil && !settings.SchedulerAt.After(at) && at.Sub(*settings.SchedulerAt) <= time.Minute {
				status.State = "enabled"
				status.Text = "Keep usage updated: enabled · checks at least 5 minutes apart"
				if p.Failures > 0 {
					status.State = "backoff"
					status.Text = "Usage updates waiting after a failed fetch; earlier evidence retains its time"
				}
			}
			break
		}
	}
}
