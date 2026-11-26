package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func TestAutomaticUsageSchedulerProcessOwnership(t *testing.T) {
	if dir := os.Getenv("LOOPTENDER_USAGE_SCHEDULER_FIXTURE"); dir != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
		defer cancel()
		ticks := time.NewTicker(5 * time.Millisecond)
		defer ticks.Stop()
		accountUsageScheduler(ctx, dir, []string{"HOME=/fictional", "XDG_STATE_HOME=/fictional"}, time.Now, func(context.Context, string, string, []string, string) ([]byte, error) {
			f, err := os.OpenFile(filepath.Join(dir, "fictional-fetch-count"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return nil, err
			}
			_, err = f.WriteString("fetch\n")
			f.Close()
			time.Sleep(30 * time.Millisecond)
			return []byte(quotaResponse), err
		}, ticks.C)
		return
	}
	dir, entry := accountFixture(t, "codex")
	fixtureEnv := []string{"HOME=/fictional", "XDG_STATE_HOME=/fictional"}
	if err := accounts.Update(dir, func(f *accounts.File) error {
		f.Entries[0].Context = accounts.Context(*f, f.Entries[0], fixtureEnv)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	selection, err := accountQuotaCapture(dir, entry.ID, fixtureEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error {
		f.Entries = []accounts.UsagePoll{{ID: entry.ID, Revision: entry.Revision, Binding: selection.Bound, Executable: accountUsageStamp(selection), Grant: strings.Repeat("a", 32), NextAt: time.Now().Add(-time.Minute)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	children := []*exec.Cmd{}
	for i := 0; i < 2; i++ {
		cmd := exec.Command(exe, "-test.run=^TestAutomaticUsageSchedulerProcessOwnership$")
		cmd.Env = append(os.Environ(), "LOOPTENDER_USAGE_SCHEDULER_FIXTURE="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	count, err := os.ReadFile(filepath.Join(dir, "fictional-fetch-count"))
	if err != nil || string(count) != "fetch\n" {
		t.Fatal("duplicate/missing cross-process acquisition", string(count), err)
	}
}

func TestAutomaticUsagePanelLifecyclePollsWithoutViewsAndNeverBlocksReads(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	var clock atomic.Int64
	clock.Store(time.Now().UTC().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	enableUsageFixture(t, dir, entry, now())
	root := t.TempDir()
	reg := dockScratch(t, root, dockNow)
	panelDir, err := panelDirectory(filepath.Join(panelShortDir(t), "panel"))
	if err != nil {
		t.Fatal(err)
	}
	ticks, idleTicks := make(chan time.Time), make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var calls atomic.Int32
	run := func(ctx context.Context, _, _ string, _ []string, _ string) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return []byte(quotaResponse), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan int, 1)
	go func() {
		done <- runPanelServer(ctx, panelServerConfig{dir: panelDir, registry: reg, idle: time.Hour, refresh: dockDefaultRefresh, staleAfter: defaultStaleAfter, now: now, ticks: idleTicks, usageDirectory: dir, usageRun: run, usageTicks: ticks})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker shutdown hung")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("panel lifecycle did not start worker without a view")
	}
	// Provider is blocked. Collection still completes from saved evidence.
	reader, _ := dockFixtureReader(t)
	reader.accountsDir = dir
	reader.now = now
	readDone := make(chan dockReport, 1)
	go func() { readDone <- reader.read(context.Background(), false) }()
	select {
	case report := <-readDone:
		if report.Accounts.Cards[0].UsageUpdating.State != "enabled" {
			t.Fatal("missing scheduler status")
		}
	case <-time.After(time.Second):
		t.Fatal("provider IO blocked collection")
	}
	release <- struct{}{}
	waitUsage(t, func() bool {
		f, _ := accounts.Load(dir)
		q := accounts.QuotaViews(dir, f, os.Environ(), now())[entry.ID]
		return q.ObservedAt != nil
	})
	waitUsage(t, func() bool {
		g, err := accounts.UsagePollGuard(dir)
		if err != nil {
			return false
		}
		g.Release()
		return true
	})
	clock.Add(int64(5 * time.Minute))
	ticks <- now()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no scheduled fetch while views closed")
	}
	release <- struct{}{}
	waitUsage(t, func() bool { f, _ := accounts.LoadUsagePolling(dir); return f.Entries[0].NextAt.After(now()) })
	if calls.Load() != 2 {
		t.Fatal("unexpected provider count", calls.Load())
	}
	// Existing idle shutdown ends ownership; polling must not keep a panel alive.
	clock.Add(int64(time.Hour))
	idleTicks <- now()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatal(code)
		}
		done <- code
	case <-time.After(5 * time.Second):
		t.Fatal("idle shutdown did not cancel scheduler")
	}
	f, _ := accounts.LoadUsagePolling(dir)
	if f.SchedulerAt != nil {
		t.Fatal("stopped owner heartbeat retained")
	}
}

func TestAutomaticUsageUpdatingProjection(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	at := time.Now().UTC()
	catalog, _ := accounts.Load(dir)
	status := func() accounts.UsageUpdating {
		v := accounts.Project(catalog, at)
		attachAccountUsageUpdating(&v, dir, os.Environ(), at)
		return *v.Cards[0].UsageUpdating
	}
	if status().State != "off" {
		t.Fatal(status())
	}
	enableUsageFixture(t, dir, entry, at)
	if status().State != "waiting" {
		t.Fatal(status())
	}
	_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.SchedulerAt = &at; return nil })
	if status().State != "enabled" {
		t.Fatal(status())
	}
	_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.Entries[0].Failures = 1; return nil })
	if status().State != "backoff" {
		t.Fatal(status())
	}
	at = at.Add(2 * time.Minute)
	if status().State != "waiting" {
		t.Fatal(status())
	}
	_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.Entries[0].Revision = strings.Repeat("f", 32); return nil })
	if status().State != "paused" {
		t.Fatal(status())
	}
}
