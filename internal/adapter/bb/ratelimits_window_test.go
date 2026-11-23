package bb

import (
	"testing"
	"time"
)

// The window BB names in its rate-limit event is kept: the provider's key,
// its label and the reason the limit was reached.
func TestRateLimitsKeepTheWindow(t *testing.T) {
	raw := []byte(`{"providerThreadId":"x","rateLimits":{"kind":"subscription-window","providerId":"claude-code","reachedReason":"seven_day","status":"blocked",
		"windows":[{"label":"5-hour limit","providerKey":"five_hour","resetsAtMs":1795000000000,"status":"blocked"},{"label":"Weekly limit","providerKey":"seven_day","resetsAtMs":1795813200000,"status":"blocked"}]}}`)
	blocked, at, w := rateLimits(raw)
	if !blocked || !at.Equal(time.UnixMilli(1795813200000)) || w.Key != "seven_day" || w.Label != "Weekly limit" || w.Reason != "seven_day" || w.Kind() != "weekly" {
		t.Fatalf("%v %v %+v", blocked, at, w)
	}
	if blocked, _, w := rateLimits([]byte(`{"rateLimits":{"status":"allowed","windows":[{"providerKey":"seven_day","status":"allowed","resetsAtMs":1}]}}`)); blocked || w.Known() {
		t.Fatalf("allowed read as blocked: %+v", w)
	}
}
