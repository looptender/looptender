// Package usageburn projects when a subscription's weekly usage window runs
// out at the rate it has been used so far. It decides nothing: a loop that
// opted in (config.UsageWatch) warns its own agent once a window from it.
package usageburn

import (
	"math"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

const week = 7 * 24 * time.Hour

// Reading is one quota reading of an account's weekly window for all
// models.
type Reading struct {
	Bucket      string
	UsedPercent float64
	ResetsAt    time.Time
}

// Weekly picks the weekly all-models window from a quota reading: a 7-day
// window with a reset time, in a bucket that is not one model's or one
// client's (Claude's seven_day_<x>). False when there is none.
func Weekly(v accounts.QuotaView) (Reading, bool) {
	if v.State != "reported" {
		return Reading{}, false
	}
	if d := v.Details; d != nil {
		for _, b := range d.Buckets {
			if strings.HasPrefix(b.Key, "seven_day_") {
				continue
			}
			for _, w := range b.Windows {
				if w.DurationMinutes != nil && *w.DurationMinutes == int64(week/time.Minute) && w.ResetsAt != nil {
					return Reading{Bucket: b.Key, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt.UTC()}, true
				}
			}
		}
		return Reading{}, false
	}
	for _, w := range v.Windows {
		if w.DurationMinutes != nil && *w.DurationMinutes == int64(week/time.Minute) && w.ResetsAt != nil {
			return Reading{Bucket: w.Name, UsedPercent: float64(w.UsedPercent), ResetsAt: w.ResetsAt.UTC()}, true
		}
	}
	return Reading{}, false
}

// Projection is the window's rate so far and when it runs out at that rate.
type Projection struct {
	// PercentPerHour is the share of the window used per hour since it
	// began (its reset less a week).
	PercentPerHour float64
	// ExhaustsAt is when the window is used up at that rate.
	ExhaustsAt time.Time
	// BeforeReset: ExhaustsAt is before the window resets.
	BeforeReset bool
}

// MinElapsed is how much of the window must have passed before it is
// projected: early in a window a little use projects a run-out far too soon.
const MinElapsed = 12 * time.Hour

// Project computes the projection at now. False when it cannot be told:
// less than MinElapsed of the window has passed, it is not running, or
// nothing of it is used yet.
func Project(r Reading, now time.Time) (Projection, bool) {
	start := r.ResetsAt.Add(-week)
	elapsed := now.Sub(start)
	if elapsed < MinElapsed || !now.Before(r.ResetsAt) || r.UsedPercent <= 0 || r.UsedPercent > 100 || math.IsNaN(r.UsedPercent) {
		return Projection{}, false
	}
	rate := r.UsedPercent / elapsed.Hours()
	left := time.Duration((100 - r.UsedPercent) / rate * float64(time.Hour))
	at := now.Add(left).UTC().Truncate(time.Minute)
	return Projection{PercentPerHour: rate, ExhaustsAt: at, BeforeReset: at.Before(r.ResetsAt)}, true
}

// Hours is the projection's time left from now, in whole hours, at least 1.
func Hours(exhaustsAt, now time.Time) int {
	return max(1, int(math.Round(exhaustsAt.Sub(now).Hours())))
}
