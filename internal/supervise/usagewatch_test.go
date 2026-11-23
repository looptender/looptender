package supervise

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// weeklyView is a Claude quota reading with its weekly window used to the
// given percent, resetting at reset, and a model's weekly window nearly full
// that the watch must not take for the all-models one.
func weeklyView(used float64, reset time.Time) accounts.QuotaView {
	week, five := int64(10080), int64(300)
	soon := reset.Add(-70 * time.Hour)
	return accounts.QuotaView{State: "reported", Details: &accounts.QuotaDetails{Buckets: []accounts.QuotaBucket{
		{Key: "five_hour", Windows: []accounts.QuotaMeter{{Name: "primary", UsedPercent: 40, DurationMinutes: &five, ResetsAt: &soon}}},
		{Key: "seven_day_opus", Windows: []accounts.QuotaMeter{{Name: "primary", UsedPercent: 99, DurationMinutes: &week, ResetsAt: &reset}}},
		{Key: "seven_day", Windows: []accounts.QuotaMeter{{Name: "primary", UsedPercent: used, DurationMinutes: &week, ResetsAt: &reset}}},
	}}}
}

type usageReads struct {
	accounts []string
	used     float64
	reset    time.Time
}

func (u *usageReads) read(_ context.Context, account string) (accounts.QuotaView, error) {
	u.accounts = append(u.accounts, account)
	return weeklyView(u.used, u.reset), nil
}

func usageWorld(t *testing.T, obs decide.Observation) (*world, *fakeAdapter, *usageReads) {
	w := newWorld(t, true, false)
	if err := state.SaveUsageWatchSetting(w.dir, &state.UsageWatchSetting{Account: "example-claude-a", Every: "30m", WarnWithin: "12h", SetAt: t0}); err != nil {
		t.Fatal(err)
	}
	// The window began four days before t0 and resets three days after.
	return w, w.primary(obs), &usageReads{reset: t0.Add(72 * time.Hour)}
}

func usageTick(t *testing.T, w *world, u *usageReads, now time.Time) Result {
	t.Helper()
	o := w.opts(now)
	o.UsageRead = u.read
	res, err := Tick(context.Background(), w.cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A coordinator idle on purpose while its lanes run is woken once, with the
// warning alone, when at the week's rate so far its account's weekly window
// runs out within warn_within and before the reset. The quota is read only
// when the watch is due, the warning is not repeated in the same window, and
// nothing is sent to anything but the coordinator.
func TestUsageWatchWarnsTheCoordinatorOncePerWindow(t *testing.T) {
	w, p, u := usageWorld(t, decide.Observation{State: decide.Idle, ActiveChildren: 2})

	// 80% in 96 h: the rest goes in 24 h, before the reset but not within 12 h.
	u.used = 80
	if res := usageTick(t, w, u, t0); res.Action != "none" || len(p.sent) != 0 || res.UsageWarned {
		t.Fatalf("not yet within warn_within: %+v %q", res, p.sent)
	}
	rec, err := state.LoadUsageWatch(w.dir)
	if err != nil || rec == nil || rec.Bucket != "seven_day" || rec.UsedPercent != 80 || !rec.ExhaustsAt.Equal(t0.Add(24*time.Hour)) || !rec.ReadAt.Equal(t0) {
		t.Fatalf("%+v %v", rec, err)
	}

	// Not due again for 30 minutes: no read.
	if usageTick(t, w, u, t0.Add(10*time.Minute)); len(u.accounts) != 1 {
		t.Fatalf("read before due: %v", u.accounts)
	}

	// 92% by 96.5 h: the rest goes in about 8 h.
	u.used = 92
	res := usageTick(t, w, u, t0.Add(30*time.Minute))
	if res.Action != "wake" || res.Reason != decide.UsageWarningWakeReason || !res.UsageWarned || len(p.sent) != 1 {
		t.Fatalf("%+v %q", res, p.sent)
	}
	text := p.sent[0]
	for _, want := range []string{"[keepgoing:usage-burn window=weekly used=92 ", "about 8 h", "not an instruction to continue the task: 2 direct child thread(s) are still running", "has not changed, paused or messaged any child thread"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "keep going") {
		t.Fatalf("the warning wake carried the task prompt: %q", text)
	}

	// The same window: read again when due, never warned again, even as the
	// reported reset drifts by seconds between reads.
	u.used = 95
	reset := u.reset
	for i, at := range []time.Duration{90 * time.Minute, 150 * time.Minute, 210 * time.Minute} {
		u.reset = reset.Add(time.Duration([]int{1, -2, 3}[i]) * time.Second)
		if res := usageTick(t, w, u, t0.Add(at)); res.UsageWarned || len(p.sent) != 1 {
			t.Fatalf("warned twice: %+v %q", res, p.sent)
		}
	}
	if len(u.accounts) != 5 {
		t.Fatalf("reads: %v", u.accounts)
	}
	for _, a := range u.accounts {
		if a != "example-claude-a" {
			t.Fatalf("read another account: %v", u.accounts)
		}
	}
	if len(w.adapters) != 1 {
		t.Fatalf("a target other than the coordinator was used: %v", w.adapters)
	}

	// A new window resets the warning.
	u.reset, u.used = t0.Add(7*24*time.Hour+time.Hour), 99
	if res := usageTick(t, w, u, t0.Add(80*time.Hour)); !res.UsageWarned || len(p.sent) != 2 {
		t.Fatalf("new window not warned: %+v", res)
	}
}

// An idle coordinator with nothing running gets the warning on the wake it
// would have had anyway, after the task prompt; a dry run reads nothing.
func TestUsageWarningRidesOnTheOrdinaryWake(t *testing.T) {
	w, p, u := usageWorld(t, decide.Observation{State: decide.Idle})
	u.used = 95
	o := w.opts(t0)
	o.UsageRead, o.DryRun = u.read, true
	if _, err := Tick(context.Background(), w.cfg, o); err != nil || len(u.accounts) != 0 {
		t.Fatalf("dry run read the quota: %v %v", u.accounts, err)
	}
	res := usageTick(t, w, u, t0)
	if res.Action != "wake" || res.Reason != "idle" || !res.UsageWarned || len(p.sent) != 1 {
		t.Fatalf("%+v", res)
	}
	if !strings.HasPrefix(p.sent[0], "keep going") || !strings.Contains(p.sent[0], "[keepgoing:usage-burn window=weekly used=95 ") {
		t.Fatalf("%q", p.sent[0])
	}
}

// Without the watch, nothing is read.
func TestNoUsageWatchReadsNothing(t *testing.T) {
	w, _, u := usageWorld(t, decide.Observation{State: decide.Idle})
	if err := state.SaveUsageWatchSetting(w.dir, nil); err != nil {
		t.Fatal(err)
	}
	u.used = 99
	if res := usageTick(t, w, u, t0); len(u.accounts) != 0 || res.UsageWarned {
		t.Fatalf("%+v %v", res, u.accounts)
	}
}

// A loop that could not be woken reads nothing: paused, waiting for a
// person, stopped.
func TestUsageWatchReadsNothingForAHeldLoop(t *testing.T) {
	for name, hold := range map[string]func(*control.File){
		"paused":        func(f *control.File) { f.Paused = true },
		"awaiting user": func(f *control.File) { f.AwaitingUser = true },
		"stopped":       func(f *control.File) { f.Stop = true },
	} {
		w, _, u := usageWorld(t, decide.Observation{State: decide.Idle})
		u.used = 99
		if _, err := control.Update(w.dir, hold); err != nil {
			t.Fatal(err)
		}
		if res := usageTick(t, w, u, t0); len(u.accounts) != 0 || res.UsageWarned {
			t.Fatalf("%s: read %v", name, u.accounts)
		}
	}
}
