package incident

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeEventsRedactionAndCompatibility(t *testing.T) {
	for _, kind := range []string{"daemon_start", "daemon_stop", "check_refused"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			r := Record{Kind: kind, Runtime: &RuntimeEvent{Reason: "PRIVATE_ERROR", Signal: "PRIVATE_SIGNAL"}, CheckStartedAt: time.Now(), DryRun: true, Outcome: "failed", TargetKey: "PRIVATE_TARGET", CountersAvailable: true, Nudges: 9, Observation: Observation{Availability: "observed", State: "idle"}, ContextBytes: 5}
			if err := Append(dir, r); err != nil {
				t.Fatal(err)
			}
			j, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			got := j.Records[0]
			if got.Runtime.Reason != "unavailable" || got.Runtime.Signal != "unavailable" || !got.DryRun || got.Observation.Availability != "not_observed" || got.CountersAvailable || got.ContextBytes != 0 || got.Tier != TierActivity {
				t.Fatal(got)
			}
			b, _ := os.ReadFile(filepath.Join(dir, FileName))
			if strings.Contains(string(b), "PRIVATE_") {
				t.Fatal("private input retained")
			}
			j.SchemaVersion = 14
			b, _ = json.Marshal(j)
			if err := os.WriteFile(filepath.Join(dir, FileName), b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir); err == nil {
				t.Fatal("new event accepted in older schema")
			}
		})
	}
}

func TestRuntimeEventsKeepKeyHistory(t *testing.T) {
	dir := t.TempDir()
	at := time.Now()
	if err := Append(dir, Record{Kind: "retarget", CheckStartedAt: at, TargetKey: TargetKey("first"), TargetKeyAfter: TargetKey("second")}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxRecords+5; i++ {
		if err := Append(dir, Record{Kind: "check_refused", CheckStartedAt: at, Runtime: &RuntimeEvent{Reason: "lock_held"}, Outcome: "failed"}); err != nil {
			t.Fatal(err)
		}
	}
	j, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Records) != MaxRecords || j.Records[0].Kind != "retarget" || j.Collapsed != 0 {
		t.Fatal("refusals evicted key history or collapsed")
	}
}

func TestRuntimeEventUpgradesSchemaFourteen(t *testing.T) {
	dir := t.TempDir()
	r := clean(sample())
	r.Sequence = 1
	legacy := Journal{SchemaVersion: 14, NextSequence: 2, Records: []Record{r}}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	j, err := Load(dir)
	if err != nil || j.SchemaVersion != 14 {
		t.Fatal(j, err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(b) {
		t.Fatal("read rewrote old journal")
	}
	if err := Append(dir, Record{Kind: "daemon_start", Runtime: &RuntimeEvent{Reason: "started"}, CheckStartedAt: time.Now(), Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	j, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.SchemaVersion != 15 || len(j.Records) != 2 || j.Records[0].Runtime != nil || j.Records[0].Sequence != 1 || j.Records[1].Sequence != 2 || j.Records[1].Runtime.Reason != "started" {
		t.Fatal(j)
	}
}
