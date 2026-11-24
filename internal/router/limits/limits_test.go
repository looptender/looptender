package limits

import (
	"strconv"
	"testing"
	"time"
)

// The header names are the ones the provider's answers carried; the values
// are made up.
func at(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func TestClaude(t *testing.T) {
	fiveHour, week := time.Date(2030, 1, 7, 15, 0, 0, 0, time.UTC), time.Date(2030, 1, 11, 4, 0, 0, 0, time.UTC)
	limited := map[string]string{
		"anthropic-ratelimit-unified-status":                  "rejected",
		"anthropic-ratelimit-unified-reset":                   at(fiveHour),
		"anthropic-ratelimit-unified-representative-claim":    "seven_day",
		"anthropic-ratelimit-unified-5h-status":               "allowed",
		"anthropic-ratelimit-unified-5h-utilization":          "0.25",
		"anthropic-ratelimit-unified-5h-reset":                at(fiveHour),
		"anthropic-ratelimit-unified-7d-status":               "rejected",
		"anthropic-ratelimit-unified-7d-utilization":          "1.0",
		"anthropic-ratelimit-unified-7d-reset":                at(week),
		"anthropic-ratelimit-unified-7d-surpassed-threshold":  "1.0",
		"anthropic-ratelimit-unified-fallback-percentage":     "0.5",
		"anthropic-ratelimit-unified-overage-status":          "rejected",
		"anthropic-ratelimit-unified-overage-disabled-reason": "example",
		"anthropic-ratelimit-unified-upgrade-paths":           "example",
		"retry-after": "3600",
	}
	r := Claude(limited)
	if r.Status != "rejected" || !r.Reset.Equal(fiveHour) || len(r.Windows) != 2 {
		t.Fatalf("%+v", r)
	}
	if w := r.Windows[0]; w.Name != "five_hour" || w.UsedPercent != 25 || !w.ResetsAt.Equal(fiveHour) || w.Model != "" {
		t.Fatalf("five-hour window %+v", w)
	}
	if w := r.Windows[1]; w.Name != "seven_day" || w.UsedPercent != 100 || !w.ResetsAt.Equal(week) {
		t.Fatalf("weekly window %+v", w)
	}

	// An ordinary answer names the weekly window only.
	r = Claude(map[string]string{
		"anthropic-ratelimit-unified-status":         "allowed",
		"anthropic-ratelimit-unified-7d-status":      "allowed",
		"anthropic-ratelimit-unified-7d-utilization": "0.41",
		"anthropic-ratelimit-unified-7d-reset":       at(week),
	})
	if r.Status != "allowed" || len(r.Windows) != 1 || r.Windows[0].Name != "seven_day" || r.Windows[0].UsedPercent != 41 {
		t.Fatalf("%+v", r)
	}

	for name, headers := range map[string]map[string]string{
		"no limit headers":      {"content-type": "text/event-stream"},
		"use that is no number": {"anthropic-ratelimit-unified-7d-utilization": "soon"},
		"negative use":          {"anthropic-ratelimit-unified-7d-utilization": "-1"},
		"no window name":        {"anthropic-ratelimit-unified--utilization": "0.5"},
		"another provider's":    {"x-codex-primary-utilization": "0.5"},
	} {
		if r := Claude(headers); len(r.Windows) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A window this code has no name for keeps the header's, a use given as
	// a percentage is taken as one, and a reset that is no time is none.
	r = Claude(map[string]string{"anthropic-ratelimit-unified-7d_example-utilization": "87", "anthropic-ratelimit-unified-7d_example-reset": "later"})
	if len(r.Windows) != 1 || r.Windows[0].Name != "7d_example" || r.Windows[0].UsedPercent != 87 || !r.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("%+v", r)
	}
}
