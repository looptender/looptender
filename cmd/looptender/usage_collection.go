package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/supervise"
)

const usageCollectionFile = "usage-collection.json"
const usageCollectionSpacing = 5 * time.Minute

type usageCollection struct {
	Version    int         `json:"version"`
	Source     string      `json:"source"`
	Cursor     int64       `json:"cursor"`
	Attempt    time.Time   `json:"attempt"`
	Success    time.Time   `json:"success"`
	Outcome    string      `json:"outcome"`
	Requests   int64       `json:"requests"`
	DurationMS int64       `json:"durationMs"`
	Bytes      int64       `json:"bytes"`
	FullPage   bool        `json:"fullPage"`
	Export     usageExport `json:"export"`
}
type usagePage struct {
	Version int           `json:"version"`
	Samples []usageSample `json:"samples"`
}
type usageFetch func(context.Context, string, string, string) ([]byte, error)

func usageSource(cfg *config.Config) string {
	if cfg.Loop.Adapter != "bb" || cfg.Loop.BB == nil || cfg.Loop.BB.Thread == "" {
		return ""
	}
	b := cfg.Loop.BB
	sum := sha256.Sum256([]byte(b.CLI + "\x00" + b.Thread + "\x00" + cfg.Loop.Account + "\x00" + cfg.Loop.Provider + "\x00" + os.Getenv("BB_SERVER_URL")))
	return hex.EncodeToString(sum[:])
}

// Called only by tickOnce while holding the existing tick/ownership fence.
// Reports never call this. No new service, account poller or lane discovery.
func collectUsageAfterTick(ctx context.Context, cfg *config.Config, res supervise.Result, dry bool) {
	if usageSource(cfg) == "" || dry || !cfg.Loop.UsageCollection || !res.Recorded || res.Target != "primary" || res.Handoff != nil {
		return
	}
	ctl, err := control.Load(cfg.Loop.StateDir)
	if err != nil || ctl.Summary() != "enabled" || ctl.PendingTransfer != nil || (ctl.BBTarget != "" && ctl.BBTarget != cfg.Loop.BB.Thread) {
		return
	}
	_ = collectUsage(ctx, cfg, time.Now().UTC(), fetchUsagePage)
}

func readUsageCollection(dir string) (usageCollection, error) {
	var r usageCollection
	f, err := openUsageEvidence(filepath.Join(dir, usageCollectionFile))
	if err != nil {
		return r, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > usageEvidenceLimit {
		return r, errors.New("unsafe collection record")
	}
	b, err := io.ReadAll(io.LimitReader(f, usageEvidenceLimit+1))
	if err != nil || len(b) > usageEvidenceLimit || !usageTypedFields(b, reflect.TypeOf(r)) {
		return r, errors.New("invalid collection record")
	}
	if json.Unmarshal(b, &r) != nil || r.Version != 1 || len(r.Export.Samples) > 128 || r.Cursor < 0 || r.Requests < 0 || r.Requests > 1e9 {
		return r, errors.New("invalid collection record")
	}
	if r.Export.Version != 1 || r.Export.Method != "observational" || r.Export.Semantics != "cumulative-inclusive-v1" || r.Export.Evidence != "automatic-BB-token-metadata" || len(r.Export.Pairs) != 0 || len(r.Export.Actions) != 0 {
		return r, errors.New("unsupported automatic contract")
	}
	if r.Outcome != "pending" && r.Outcome != "recorded" && r.Outcome != "unavailable" {
		return r, errors.New("invalid outcome")
	}
	if r.DurationMS < 0 || r.Bytes < 0 || r.Bytes > 32<<10 {
		return r, errors.New("invalid overhead")
	}
	return r, nil
}
func saveUsageCollection(dir string, r usageCollection) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > usageEvidenceLimit {
		return errors.New("collection limit")
	}
	return snapshot.WriteFileAtomic(filepath.Join(dir, usageCollectionFile), b, 0600, true)
}

func collectUsage(ctx context.Context, cfg *config.Config, now time.Time, fetch usageFetch) error {
	source := usageSource(cfg)
	if source == "" {
		return nil
	}
	dir := cfg.Loop.StateDir
	r, err := readUsageCollection(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	} // damage holds; no rescan
	if !r.Attempt.IsZero() && now.Sub(r.Attempt) < usageCollectionSpacing {
		return nil
	}
	if r.Source != source {
		r = usageCollection{Version: 1, Source: source, Export: usageExport{Version: 1, Loop: cfg.Loop.Name, Method: "observational", Evidence: "automatic-BB-token-metadata", Semantics: "cumulative-inclusive-v1"}}
	}
	// The attempt and its spacing survive failures/restarts, before any query.
	r.Attempt = now
	r.Outcome = "pending"
	r.Requests++
	if err := saveUsageCollection(dir, r); err != nil {
		return err
	}
	after := "latest"
	if r.Cursor > 0 {
		after = strconv.FormatInt(r.Cursor, 10)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	raw, fetchErr := fetch(ctx, cfg.Loop.BB.CLI, cfg.Loop.BB.Thread, after)
	r.DurationMS = time.Since(start).Milliseconds()
	r.Bytes = int64(len(raw))
	r.Outcome = "unavailable"
	var page usagePage
	if fetchErr == nil && len(raw) <= 32<<10 && usageTypedFields(raw, reflect.TypeOf(page)) && json.Unmarshal(raw, &page) == nil && page.Version == 1 && len(page.Samples) <= 32 {
		next, e := applyUsagePage(r, page, now)
		if e == nil {
			r = next
			r.Success = now
			r.Outcome = "recorded"
		}
	}
	return saveUsageCollection(dir, r)
}

func applyUsagePage(r usageCollection, p usagePage, now time.Time) (usageCollection, error) {
	// Strict increasing sequences reject conflicting repeats or reordering as a
	// whole page. Replayed committed pages cannot add usage or advance a cursor.
	last := r.Cursor
	if last == 0 && len(p.Samples) > 1 {
		return r, errors.New("bootstrap limit")
	}
	for _, s := range p.Samples {
		if s.Seq <= last || s.At.IsZero() || s.At.After(now) || !usageEvidenceLabel.MatchString(s.Stream) || s.Provider != "unavailable" || s.Model != "unavailable" || !validCounters(s.Counters) {
			return r, errors.New("invalid page")
		}
		last = s.Seq
	}
	rows := append(append([]usageSample{}, r.Export.Samples...), p.Samples...)
	// A bounded rolling view, not an all-history ledger. Retained first samples
	// remain baselines, and old intervals are never claimed as complete coverage.
	cut := now.Add(-24 * time.Hour)
	kept := []usageSample{}
	for _, s := range rows {
		if !s.At.Before(cut) {
			kept = append(kept, s)
		}
	}
	if len(kept) > 128 {
		kept = kept[len(kept)-128:]
	}
	// Bound distinct sessions too; drop oldest samples until at most 16 remain.
	for {
		ids := map[string]bool{}
		for _, s := range kept {
			ids[s.Stream] = true
		}
		if len(ids) <= 16 {
			break
		}
		kept = kept[1:]
	}
	r.Export.Samples = kept
	r.Export.From = cut
	r.Export.To = now
	raw, _ := json.Marshal(r.Export)
	if parseUsageEvidence(raw, r.Export.Loop, now).Availability != "recorded" {
		return r, errors.New("invalid aggregate")
	}
	r.Cursor = last
	r.FullPage = len(p.Samples) == 32
	return r, nil
}

func fetchUsagePage(ctx context.Context, cli, thread, after string) ([]byte, error) {
	if cli == "" {
		cli = config.DefaultBBCLI
	}
	cmd := exec.CommandContext(ctx, cli, "looptender", "usage-events", thread, after)
	cmd.WaitDelay = 100 * time.Millisecond
	out := &doctorBuffer{remaining: 32 << 10}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if out.exceeded {
		return nil, errors.New("usage output limit")
	}
	return bytes.Clone(out.Bytes()), err
}

func automaticUsageMeasurement(cfg *config.Config, now time.Time) usageMeasurement {
	if usageSource(cfg) == "" {
		return unavailableUsage("Automatic collection unavailable: only the configured primary BB thread is supported (Codex metadata normalization).")
	}
	r, err := readUsageCollection(cfg.Loop.StateDir)
	if err != nil || r.Source != usageSource(cfg) {
		return unavailableUsage("Automatic collection unavailable: no matching private source record. Imported evidence is retained separately.")
	}
	raw, _ := json.Marshal(r.Export)
	out := parseUsageEvidence(raw, cfg.Loop.Name, now)
	if len(out.Details) > 1 {
		out.Details[1] = "Collector-generated rolling evidence: consecutive cumulative deltas only; initial snapshots, resets and missing fields are excluded. Retention completeness and historical model attribution are unknown. Cached input and reasoning are components, not additional tokens."
	}
	if len(r.Export.Samples) == 0 {
		out.Availability = "unavailable"
		out.Details = []string{"No retained cumulative samples; actual usage is unavailable, not zero."}
	}
	fresh := "stale"
	if !r.Success.IsZero() && now.Sub(r.Success) >= 0 && now.Sub(r.Success) <= 2*usageCollectionSpacing {
		fresh = "fresh"
	}
	stamp := "unavailable"
	if !r.Success.IsZero() {
		stamp = r.Success.UTC().Format(time.RFC3339)
	}
	out.Details = append([]string{fmt.Sprintf("Automatic BB Codex token metadata; %s; last successful read %s; last attempt %s. Scope: configured primary thread only, no lanes or account-wide totals.", fresh, stamp, r.Outcome), fmt.Sprintf("Rolling lower bound: up to 128 snapshots / 24 hours / 16 sessions. Initial history skipped; source retention gaps unknown; full page pending: %t. Historical provider/model attribution and accepted work unavailable. Collection attempts %d; last read %d ms / %d bytes; no model calls. Collection token overhead unavailable.", r.FullPage, r.Requests, r.DurationMS, r.Bytes)}, out.Details...)
	if b, err := json.Marshal(out); err != nil || len(b) > 6<<10 {
		return unavailableUsage("Automatic BB collection recorded; projected view exceeds bounds. Coverage and savings unavailable.")
	}
	return out
}
