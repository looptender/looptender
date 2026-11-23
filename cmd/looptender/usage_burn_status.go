package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/usageburn"
)

// usageWatchAccountsDir is the accounts catalog the usage watch reads; empty
// is the user's own. Tests set it.
var usageWatchAccountsDir string

// usageWatchQuotaRead is the read a check's usage watch makes: the catalog's
// quota read of the one entry, with no model request.
var usageWatchQuotaRead accountQuotaRun = accountQuotaRead

func tickUsageRead(ctx context.Context, account string) (accounts.QuotaView, error) {
	dir, err := accountsDirectory(usageWatchAccountsDir)
	if err != nil {
		return accounts.QuotaView{}, err
	}
	return accountQuotaRefresh(ctx, dir, account, nil, usageWatchQuotaRead, os.Environ(), time.Now)
}

// usageBurnView is what status and the panel say of the usage watch: shown
// only while, at the weekly window's rate so far, it runs out before it
// resets, from a reading no older than twice the watch's interval.
type usageBurnView struct {
	Words       string    `json:"words"`
	UsedPercent int       `json:"usedPercent"`
	Hours       int       `json:"hours"`
	ExhaustsAt  time.Time `json:"exhaustsAt"`
	ResetsAt    time.Time `json:"resetsAt"`
	ReadAt      time.Time `json:"readAt"`
	Warned      bool      `json:"warned,omitempty"`
}

func describeUsageBurn(l config.Loop, now time.Time) *usageBurnView {
	w := l.UsageWatch
	if w == nil {
		return nil
	}
	rec, err := state.LoadUsageWatch(l.StateDir)
	if err != nil || rec == nil || rec.Account != w.Account || rec.ExhaustsAt.IsZero() || !now.Before(rec.ResetsAt) ||
		rec.ReadAt.After(now) || now.Sub(rec.ReadAt) > 2*w.Every.Duration {
		return nil
	}
	h := usageburn.Hours(rec.ExhaustsAt, now)
	if !now.Before(rec.ExhaustsAt) {
		h = 0
	}
	v := &usageBurnView{UsedPercent: int(rec.UsedPercent), Hours: h, ExhaustsAt: rec.ExhaustsAt, ResetsAt: rec.ResetsAt, ReadAt: rec.ReadAt, Warned: rec.WarnedFor.Equal(rec.ResetsAt)}
	v.Words = fmt.Sprintf("At this rate the weekly window is used up in about %d h, before it resets.", h)
	if h == 0 {
		v.Words = "At this rate the weekly window is used up now, before it resets."
	}
	return v
}

func printUsageBurn(w io.Writer, v *usageBurnView) {
	if v == nil {
		return
	}
	fmt.Fprintf(w, "usage: %s %d%% used, read %s; resets %s\n", v.Words, v.UsedPercent, v.ReadAt.Format(time.RFC3339), v.ResetsAt.Format(time.RFC3339))
}
