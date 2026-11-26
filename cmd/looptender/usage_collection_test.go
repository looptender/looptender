package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/snapshot"
)

func collectionFixture(t *testing.T) (*config.Config, time.Time) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Loop.Name = "example"
	cfg.Loop.StateDir = t.TempDir()
	cfg.Loop.Adapter = "bb"
	cfg.Loop.BB = &config.BB{Thread: "thr_example"}
	cfg.Loop.UsageCollection = true
	return cfg, time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
}
func collectionSample(seq, n int64, at time.Time) usageSample {
	return usageSample{Stream: "example-session", Provider: "unavailable", Model: "unavailable", Seq: seq, At: at, Counters: usageCounters{Input: &n}}
}
func pageFetch(samples ...usageSample) usageFetch {
	return func(context.Context, string, string, string) ([]byte, error) {
		return json.Marshal(usagePage{Version: 1, Samples: samples})
	}
}
func TestUsageCollectionRestartReplayAndFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native safe reader unavailable")
	}
	cfg, now := collectionFixture(t)
	ctx := context.Background()
	first := collectionSample(10, 100, now)
	if err := collectUsage(ctx, cfg, now, pageFetch(first)); err != nil {
		t.Fatal(err)
	}
	// Fresh function invocation reads the committed position after a restart.
	calls := 0
	fetch := func(_ context.Context, _, thread, after string) ([]byte, error) {
		calls++
		if thread != "thr_example" || after != "10" {
			t.Fatalf("scope/cursor: %s %s", thread, after)
		}
		return pageFetch(collectionSample(20, 160, now.Add(5*time.Minute)))(ctx, "", "", "")
	}
	if err := collectUsage(ctx, cfg, now.Add(time.Minute), fetch); err != nil || calls != 0 {
		t.Fatal("cooldown", err, calls)
	}
	later := now.Add(5 * time.Minute)
	if err := collectUsage(ctx, cfg, later, fetch); err != nil {
		t.Fatal(err)
	}
	r, _ := readUsageCollection(cfg.Loop.StateDir)
	if r.Cursor != 20 || len(r.Export.Samples) != 2 {
		t.Fatal(r)
	}
	view := automaticUsageMeasurement(cfg, later)
	if !strings.Contains(strings.Join(view.Trend, " "), "input 60") {
		t.Fatal(view.Trend)
	}
	// A repeated page is rejected without recounting and the cursor survives.
	if err := collectUsage(ctx, cfg, later.Add(5*time.Minute), pageFetch(collectionSample(20, 999, later))); err != nil {
		t.Fatal(err)
	}
	r, _ = readUsageCollection(cfg.Loop.StateDir)
	if r.Cursor != 20 || len(r.Export.Samples) != 2 || r.Outcome != "unavailable" {
		t.Fatal(r)
	}
	fail := func(context.Context, string, string, string) ([]byte, error) { return nil, errors.New("private error") }
	if err := collectUsage(ctx, cfg, later.Add(10*time.Minute), fail); err != nil {
		t.Fatal(err)
	}
	view = automaticUsageMeasurement(cfg, later.Add(time.Hour))
	if !strings.Contains(view.Details[0], "stale") || strings.Contains(strings.Join(view.Details, " "), "private error") {
		t.Fatal(view)
	}
}
func TestUsageCollectionSourceChangeDamageAndWriteFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native safe reader unavailable")
	}
	cfg, now := collectionFixture(t)
	ctx := context.Background()
	if err := collectUsage(ctx, cfg, now, pageFetch(collectionSample(10, 100, now))); err != nil {
		t.Fatal(err)
	}
	cfg.Loop.BB.Thread = "thr_new_example"
	if automaticUsageMeasurement(cfg, now).Availability != "unavailable" {
		t.Fatal("old source exposed")
	}
	calls := 0
	fetch := func(_ context.Context, _, thread, after string) ([]byte, error) {
		calls++
		if thread != "thr_new_example" || after != "latest" {
			t.Fatal(thread, after)
		}
		return pageFetch(collectionSample(90, 500, now.Add(5*time.Minute)))(ctx, "", "", "")
	}
	if err := collectUsage(ctx, cfg, now.Add(5*time.Minute), fetch); err != nil {
		t.Fatal(err)
	}
	r, _ := readUsageCollection(cfg.Loop.StateDir)
	if len(r.Export.Samples) != 1 || r.Cursor != 90 {
		t.Fatal(r)
	}
	snapshot.InjectWriteFaults(func(string) (int, error) { return 0, snapshot.ENOSPC })
	t.Cleanup(func() { snapshot.InjectWriteFaults(nil) })
	if err := collectUsage(ctx, cfg, now.Add(10*time.Minute), fetch); err == nil || calls != 1 {
		t.Fatal("query before durable attempt", err, calls)
	}
	snapshot.InjectWriteFaults(nil)
	os.WriteFile(filepath.Join(cfg.Loop.StateDir, usageCollectionFile), []byte(`{"version":1,"Version":1}`), 0600)
	if err := collectUsage(ctx, cfg, now.Add(15*time.Minute), fetch); err == nil || calls != 1 {
		t.Fatal("damage caused scan", err, calls)
	}
}
func TestUsageCollectionResetsGapsAndBounds(t *testing.T) {
	cfg, now := collectionFixture(t)
	r := usageCollection{Version: 1, Source: usageSource(cfg), Export: usageExport{Version: 1, Loop: "example", Method: "observational", Evidence: "automatic-BB-token-metadata", Semantics: "cumulative-inclusive-v1"}}
	var err error
	r, err = applyUsagePage(r, usagePage{Samples: []usageSample{collectionSample(1, 100, now)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = applyUsagePage(r, usagePage{Samples: []usageSample{collectionSample(5, 20, now), collectionSample(9, 30, now)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r.Export)
	view := parseUsageEvidence(raw, "example", now)
	if !strings.Contains(strings.Join(view.Trend, " "), "input 10") {
		t.Fatal(view.Trend)
	}
	// A retention gap ages out the old baseline; it never becomes a delta.
	later := now.Add(25 * time.Hour)
	r, err = applyUsagePage(r, usagePage{Samples: []usageSample{collectionSample(100, 999, later)}}, later)
	if err != nil || len(r.Export.Samples) != 1 {
		t.Fatal(err, r)
	}
	for j := 0; j < 8; j++ {
		samples := []usageSample{}
		for i := 0; i < 32; i++ {
			seq := int64(101 + j*32 + i)
			samples = append(samples, collectionSample(seq, 1000+seq, later))
		}
		r, err = applyUsagePage(r, usagePage{Samples: samples}, later)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Export.Samples) != 128 || !r.FullPage {
		t.Fatal("unbounded record", r)
	}
	// Future, unordered and conflicting pages must leave the committed cursor.
	for _, samples := range [][]usageSample{{collectionSample(r.Cursor+1, 10, later.Add(time.Second))}, {collectionSample(r.Cursor+2, 10, later), collectionSample(r.Cursor+1, 11, later)}} {
		if _, err = applyUsagePage(r, usagePage{Samples: samples}, later); err == nil {
			t.Fatal("bad page accepted")
		}
	}
}

func TestUsageCollectionRejectsInventedEstimatesAndPrivateOutcome(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native safe reader unavailable")
	}
	cfg, now := collectionFixture(t)
	if err := collectUsage(context.Background(), cfg, now, pageFetch(collectionSample(10, 100, now))); err != nil {
		t.Fatal(err)
	}
	original, err := readUsageCollection(cfg.Loop.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"estimate", "outcome"} {
		r := original
		if kind == "estimate" {
			r.Export.Method = "controlled-replay"
			r.Export.Pairs = []usagePair{{CoverageComplete: true}}
		} else {
			r.Outcome = "/private/example"
		}
		if err := saveUsageCollection(cfg.Loop.StateDir, r); err != nil {
			t.Fatal(err)
		}
		view := automaticUsageMeasurement(cfg, now)
		b, _ := json.Marshal(view)
		if view.Availability != "unavailable" || strings.Contains(string(b), "/private/") {
			t.Fatal(view)
		}
	}
}
