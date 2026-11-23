package usageburn

import (
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func TestWeeklyTakesTheAllModelsWindow(t *testing.T) {
	week := int64(10080)
	reset := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	v := accounts.QuotaView{State: "reported", Details: &accounts.QuotaDetails{Buckets: []accounts.QuotaBucket{
		{Key: "seven_day_sonnet", Windows: []accounts.QuotaMeter{{Name: "primary", UsedPercent: 90, DurationMinutes: &week, ResetsAt: &reset}}},
		{Key: "seven_day", Windows: []accounts.QuotaMeter{{Name: "primary", UsedPercent: 30, DurationMinutes: &week, ResetsAt: &reset}}},
	}}}
	r, ok := Weekly(v)
	if !ok || r.Bucket != "seven_day" || r.UsedPercent != 30 || !r.ResetsAt.Equal(reset) {
		t.Fatalf("%+v %v", r, ok)
	}
	codex := accounts.QuotaView{State: "reported", Windows: []accounts.QuotaWindow{{Name: "secondary", UsedPercent: 12, DurationMinutes: &week, ResetsAt: &reset}}}
	if r, ok := Weekly(codex); !ok || r.Bucket != "secondary" || r.UsedPercent != 12 {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := Weekly(accounts.QuotaView{State: "unavailable"}); ok {
		t.Fatal("unavailable reading used")
	}
}

func TestProjectAtTheWindowsRate(t *testing.T) {
	reset := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	start := reset.Add(-week)
	// Half used in two days: 25% a day, the rest in two more, before the reset.
	p, ok := Project(Reading{UsedPercent: 50, ResetsAt: reset}, start.Add(48*time.Hour))
	if !ok || !p.BeforeReset || !p.ExhaustsAt.Equal(start.Add(96*time.Hour)) || Hours(p.ExhaustsAt, start.Add(48*time.Hour)) != 48 {
		t.Fatalf("%+v %v", p, ok)
	}
	// A fifth in four days: it lasts the week.
	if p, ok := Project(Reading{UsedPercent: 20, ResetsAt: reset}, start.Add(96*time.Hour)); !ok || p.BeforeReset {
		t.Fatalf("%+v %v", p, ok)
	}
	for name, at := range map[string]time.Time{"first hour": start.Add(30 * time.Minute), "after reset": reset.Add(time.Minute)} {
		if _, ok := Project(Reading{UsedPercent: 10, ResetsAt: reset}, at); ok {
			t.Fatalf("%s projected", name)
		}
	}
	if _, ok := Project(Reading{UsedPercent: 0, ResetsAt: reset}, start.Add(48*time.Hour)); ok {
		t.Fatal("nothing used, projected")
	}
}
