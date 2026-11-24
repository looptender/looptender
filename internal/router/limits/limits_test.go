package limits

import (
	"strconv"
	"testing"
	"time"
)

var now = time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)

func at(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

// The header names are the ones the provider's answers carried; the values
// are made up, in the one form there is evidence for.
func TestClaude(t *testing.T) {
	fiveHour, week := now.Add(3*time.Hour), now.Add(4*24*time.Hour)
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
		"anthropic-ratelimit-unified-overage-utilization":     "1.0",
		"anthropic-ratelimit-unified-overage-disabled-reason": "example",
		"anthropic-ratelimit-unified-upgrade-paths":           "example",
		"retry-after": "3600",
	}
	r := Claude(limited, now)
	if r.Status != "rejected" || !r.Reset.Equal(fiveHour) || len(r.Windows) != 2 {
		t.Fatalf("%+v (an overage figure must not become a window)", r)
	}
	if w := r.Windows[0]; w.Name != "five_hour" || w.UsedPercent != 25 || !w.ResetsAt.Equal(fiveHour) || w.Model != "" || !w.At.Equal(now) {
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
	}, now)
	if r.Status != "allowed" || len(r.Windows) != 1 || r.Windows[0].Name != "seven_day" || r.Windows[0].UsedPercent != 41 {
		t.Fatalf("%+v", r)
	}
}

// Anything not in the expected form is unknown: no window, never a guess.
func TestClaudeUnknownIsNotAReading(t *testing.T) {
	const use = "anthropic-ratelimit-unified-7d-utilization"
	for _, v := range []string{"", "NaN", "nan", "Inf", "+Inf", "-0.5", "-1", "+0.5", "1e2", "0x1p-2", "87", "41%", "2.01", "soon", " 0.5", "0.5 ", "0,5", ".", "1.2.3", "0.123456789012", "٠.٥", "5.", "2.5"} {
		if r := Claude(map[string]string{use: v}, now); len(r.Windows) != 0 {
			t.Errorf("use %q was read as %+v", v, r.Windows)
		}
	}
	for _, v := range []string{"0", "0.0", "1", "1.0", "0.97", "1.5", "2", ".5", "1."} {
		if r := Claude(map[string]string{use: v}, now); len(r.Windows) != 1 {
			t.Errorf("use %q was not read", v)
		}
	}
	// Only the windows there are names for.
	for name, headers := range map[string]map[string]string{
		"no limit headers":         {"content-type": "text/event-stream"},
		"an overage figure":        {"anthropic-ratelimit-unified-overage-utilization": "1.0"},
		"a window with no name":    {"anthropic-ratelimit-unified--utilization": "0.5"},
		"a window not known here":  {"anthropic-ratelimit-unified-7d_example-utilization": "1.0"},
		"another provider's":       {"x-codex-primary-utilization": "0.5"},
		"the threshold, not a use": {"anthropic-ratelimit-unified-7d-surpassed-threshold": "1.0"},
	} {
		if r := Claude(headers, now); len(r.Windows) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A reset that cannot be believed is no reset; the use is still read.
	for _, v := range []string{"", "later", "-5", "1.5e9", "0", "12", at(now.Add(-48 * time.Hour)), at(now.Add(60 * 24 * time.Hour)), "99999999999", "253402300800000", strconv.FormatInt(now.UnixMilli(), 10)} {
		r := Claude(map[string]string{use: "1.0", "anthropic-ratelimit-unified-7d-reset": v, "anthropic-ratelimit-unified-reset": v}, now)
		if len(r.Windows) != 1 || !r.Windows[0].ResetsAt.IsZero() || !r.Reset.IsZero() {
			t.Errorf("reset %q was believed: %+v", v, r)
		}
	}
	// A status that is not one of the three words is none.
	if r := Claude(map[string]string{"anthropic-ratelimit-unified-status": "probably"}, now); r.Status != "" {
		t.Errorf("status %q", r.Status)
	}
}
