package decide

import "testing"

func TestLimitWindowWords(t *testing.T) {
	for _, c := range []struct {
		key, label, reason, kind, model, words string
	}{
		{"five_hour", "Session limit", "five_hour", "five_hour", "", "5-hour limit"},
		{"seven_day", "Weekly limit", "seven_day", "weekly", "", "weekly limit (all models)"},
		{"seven_day_opus", "Weekly limit (Opus)", "", "weekly_model", "opus", "weekly limit for opus"},
		{"", "", "seven_day", "weekly", "", "weekly limit (all models)"},
		{"overage", "Extra usage", "", "other", "", "Extra usage"},
		{"", "", "", "", "", "usage limit"},
	} {
		w := CleanLimitWindow(c.key, c.label, c.reason)
		if w.Kind() != c.kind || w.Model() != c.model || w.Words() != c.words {
			t.Errorf("%+v: kind %q model %q words %q", c, w.Kind(), w.Model(), w.Words())
		}
	}
	// Anything outside the expected shape is dropped, never passed on.
	if w := CleanLimitWindow("Seven Day!", "see https://example.com/x", "a b"); w.Known() {
		t.Errorf("kept %+v", w)
	}
}
