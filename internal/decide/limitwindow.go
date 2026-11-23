package decide

import (
	"regexp"
	"strings"
)

// LimitWindow is the provider's usage window a limit is reached in, as BB's
// rate-limit events name it: Key is the provider's own key for the window
// (five_hour, seven_day, or a model's weekly window such as
// seven_day_opus), Label its label ("Weekly limit"), and Reason the key the
// provider gave for reaching the limit. All three are bounded to a short,
// plain vocabulary; anything else is dropped. Zero when unknown.
type LimitWindow struct {
	Key    string `json:"key,omitempty"`
	Label  string `json:"label,omitempty"`
	Reason string `json:"reason,omitempty"`
}

var (
	limitKey   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	limitLabel = regexp.MustCompile(`^[A-Za-z0-9 ,.()'/-]{1,60}$`)
)

// CleanLimitWindow keeps only values in the expected shape.
func CleanLimitWindow(key, label, reason string) LimitWindow {
	w := LimitWindow{}
	if limitKey.MatchString(key) {
		w.Key = key
	}
	if label = strings.TrimSpace(label); limitLabel.MatchString(label) {
		w.Label = label
	}
	if limitKey.MatchString(reason) {
		w.Reason = reason
	}
	return w
}

// Known says whether anything about the window is known.
func (w LimitWindow) Known() bool { return w.Key != "" || w.Label != "" || w.Reason != "" }

// Kind is the window in a fixed vocabulary: five_hour, weekly (all models),
// weekly_model (one model's weekly window), or other; empty when unknown.
func (w LimitWindow) Kind() string {
	k := w.Key
	if k == "" {
		k = w.Reason
	}
	switch {
	case k == "":
		return ""
	case k == "five_hour":
		return "five_hour"
	case k == "seven_day":
		return "weekly"
	case strings.HasPrefix(k, "seven_day_"):
		return "weekly_model"
	}
	return "other"
}

// Model is the model a model-specific window names (seven_day_opus gives
// opus), or "".
func (w LimitWindow) Model() string {
	k := w.Key
	if k == "" {
		k = w.Reason
	}
	if m, ok := strings.CutPrefix(k, "seven_day_"); ok {
		return m
	}
	return ""
}

// Words is the window in one phrase for people: "5-hour limit", "weekly
// limit (all models)", "weekly limit for opus", or the provider's label.
func (w LimitWindow) Words() string {
	switch w.Kind() {
	case "five_hour":
		return "5-hour limit"
	case "weekly":
		return "weekly limit (all models)"
	case "weekly_model":
		return "weekly limit for " + w.Model()
	}
	if w.Label != "" {
		return w.Label
	}
	return "usage limit"
}
