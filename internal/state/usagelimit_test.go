package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/decide"
)

// TestUsageLimitIsItsOwnRecord pins that the usage limit needs no state
// format change: state.json has no field for it, a state.json written
// without one loads as before, and a build that knows only state.json
// (Load then Save, as every older build does on each check) leaves the
// usage-limit record exactly as it was. After a rollback the record is
// therefore kept, unread, and the next newer check replaces or removes it.
func TestUsageLimitIsItsOwnRecord(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	u := &UsageLimit{Target: "primary", Provider: "claude-code", Window: decide.LimitWindow{Key: "seven_day", Label: "Weekly"}, ResetsAt: at.Add(48 * time.Hour), ObservedAt: at}
	if err := SaveUsageLimit(dir, u); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, UsageLimitFileName))
	if err != nil {
		t.Fatal(err)
	}

	// An older build's state.json, with no usage limit anywhere in it.
	old := []byte(`{"lastCheck":{"at":"2026-01-05T08:55:00Z"},"agentId":"thr_example1"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, st); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["usageLimit"]; ok {
		t.Fatalf("state.json carries the usage limit: %s", b)
	}
	after, err := os.ReadFile(filepath.Join(dir, UsageLimitFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a state.json rewrite changed the record: %s %v", after, err)
	}
	got, err := LoadUsageLimit(dir)
	if err != nil || got == nil || got.Window != u.Window || !got.ResetsAt.Equal(u.ResetsAt) {
		t.Fatalf("%+v %v", got, err)
	}

	if err := SaveUsageLimit(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUsageLimit(dir); got != nil || err != nil {
		t.Fatalf("not removed: %+v %v", got, err)
	}
	if err := SaveUsageLimit(dir, nil); err != nil {
		t.Fatalf("removing an absent record: %v", err)
	}
}
