package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func enableUsageFixture(t *testing.T, dir string, e accounts.Entry, at time.Time) {
	t.Helper()
	s, err := accountQuotaCapture(dir, e.ID, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	err = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error {
		f.Entries = append(f.Entries, accounts.UsagePoll{ID: e.ID, Revision: e.Revision, Binding: s.Bound, Executable: accountUsageStamp(s), Grant: strings.Repeat("b", 32), NextAt: at})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestAutomaticUsageUIRefreshAcquiresNewProviderReading(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	base, _, d := dockServe(t)
	d.accountsDir = dir
	d.usagePolling = true
	at := dockNow
	d.now = func() time.Time { return at }
	calls := 0
	d.quotaRun = func(ctx context.Context, provider, cli string, env []string, wd string) ([]byte, error) {
		calls++
		if provider != "codex" || cli != entry.CLI || wd != entry.Workdir {
			t.Fatal("wrong selection")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded read")
		}
		return []byte(strings.Replace(quotaResponse, `"usedPercent":0`, `"usedPercent":`+[]string{"10", "20"}[calls-1], 1)), nil
	}
	read := func() *accounts.QuotaView {
		d.forget()
		resp, raw := dockRequest(t, "GET", base+dockAPIPath, nil)
		var main dockReport
		if resp.StatusCode != 200 || json.Unmarshal([]byte(raw), &main) != nil {
			t.Fatal("dock reply", resp.StatusCode)
		}
		resp, raw = dockRequest(t, "GET", base+dockAccountsPath+"?reference="+main.AccountsReference, nil)
		var saved dockAccountSnapshot
		if resp.StatusCode != 200 || json.Unmarshal([]byte(raw), &saved) != nil || saved.Accounts == nil || len(saved.Accounts.Cards) != 1 {
			t.Fatal("saved reply", resp.StatusCode)
		}
		return saved.Accounts.Cards[0].Quota
	}
	read()
	if calls != 0 {
		t.Fatal("unconfigured UI called provider")
	}
	enableUsageFixture(t, dir, entry, at)
	first := read()
	if calls != 1 || first == nil || first.ObservedAt == nil || !first.ObservedAt.Equal(at) || first.Windows[0].UsedPercent != 10 {
		t.Fatal("first UI read did not acquire", calls, first)
	}
	at = at.Add(time.Minute)
	read()
	if calls != 1 {
		t.Fatal("cache hit bypassed cadence")
	}
	at = at.Add(4 * time.Minute)
	second := read()
	if calls != 2 || !second.ObservedAt.Equal(at) || second.Windows[0].UsedPercent != 20 {
		t.Fatal("refresh reread cache instead of provider", calls, second)
	}
	// A separately collected native menu consumes the same newly published cache,
	// without another provider call in this cadence.
	native := buildPanelList(d.read(context.Background(), false))
	if calls != 2 || native.AccountQuotas == nil || !native.AccountQuotas.Accounts[0].Summary.ObservedAt.Equal(at) {
		t.Fatal("menu did not share acquired reading")
	}
	d.usagePolling = false
	at = at.Add(5 * time.Minute)
	read()
	if calls != 2 {
		t.Fatal("offline read acquired usage")
	}
}
func TestAutomaticUsageFailuresBackoffAndKeepOldTimestamps(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	at := dockNow
	enableUsageFixture(t, dir, entry, at)
	calls := 0
	fail := false
	run := func(context.Context, string, string, []string, string) ([]byte, error) {
		calls++
		if fail {
			return nil, context.DeadlineExceeded
		}
		return []byte(quotaResponse), nil
	}
	poll := func() {
		accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
	}
	poll()
	saved := at
	fail = true
	at = at.Add(5 * time.Minute)
	poll()
	f, _ := accounts.Load(dir)
	q := accounts.QuotaViews(dir, f, os.Environ(), at)[entry.ID]
	if calls != 2 || !q.ObservedAt.Equal(saved) || q.FailedRefreshReason != "timeout" {
		t.Fatal("failure replaced earlier evidence", calls, q)
	}
	settings, _ := accounts.LoadUsagePolling(dir)
	if settings.Entries[0].Failures != 1 || !settings.Entries[0].NextAt.Equal(at.Add(10*time.Minute)) {
		t.Fatal("failure not backed off", settings)
	}
	at = at.Add(9 * time.Minute)
	poll()
	if calls != 2 {
		t.Fatal("backoff bypassed")
	}
	at = at.Add(time.Minute)
	poll()
	settings, _ = accounts.LoadUsagePolling(dir)
	if calls != 3 || !settings.Entries[0].NextAt.Equal(at.Add(20*time.Minute)) {
		t.Fatal("repeated failure not backed off")
	}
	for i := 0; i < 8; i++ {
		at = settings.Entries[0].NextAt
		poll()
		settings, _ = accounts.LoadUsagePolling(dir)
	}
	if settings.Entries[0].NextAt.Sub(at) != time.Hour {
		t.Fatal("unbounded backoff")
	}
	fail = false
	at = settings.Entries[0].NextAt
	poll()
	settings, _ = accounts.LoadUsagePolling(dir)
	if settings.Entries[0].Failures != 0 || settings.Entries[0].NextAt.Sub(at) != 5*time.Minute {
		t.Fatal("success did not restore cadence")
	}
}
func TestAutomaticUsageInflightDedupCrashReservationAndRevocation(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	at := dockNow
	enableUsageFixture(t, dir, entry, at)
	started, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	run := func(context.Context, string, string, []string, string) ([]byte, error) {
		calls.Add(1)
		close(started)
		<-finish
		return []byte(quotaResponse), nil
	}
	go func() {
		defer close(done)
		accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
	}()
	<-started
	settings, _ := accounts.LoadUsagePolling(dir)
	if settings.Entries[0].AttemptAt == nil || !settings.Entries[0].NextAt.Equal(at.Add(5*time.Minute)) {
		t.Fatal("external read began before durable reservation")
	}
	accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
	if calls.Load() != 1 {
		t.Fatal("duplicate provider call")
	}
	if err := accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.Entries = []accounts.UsagePoll{}; return nil }); err != nil {
		t.Fatal(err)
	}
	close(finish)
	<-done
	settings, _ = accounts.LoadUsagePolling(dir)
	if len(settings.Entries) != 0 {
		t.Fatal("completion restored revoked grant")
	}
	accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at.Add(time.Hour) }, run)
	if calls.Load() != 1 {
		t.Fatal("revoked selection was retried")
	}
}
func TestAutomaticUsageSelectionDriftCorruptionAndCLIRevocation(t *testing.T) {
	for _, kind := range []string{"revision", "removed", "environment", "executable", "same_metadata_replacement", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			dir, entry := accountFixture(t, "codex")
			at := dockNow
			enableUsageFixture(t, dir, entry, at)
			environment := os.Environ()
			switch kind {
			case "revision":
				_ = accounts.Update(dir, func(f *accounts.File) error { f.Entries[0].Revision = strings.Repeat("c", 32); return nil })
			case "removed":
				_ = accounts.Update(dir, func(f *accounts.File) error { f.Entries = []accounts.Entry{}; return nil })
			case "environment":
				environment = append(environment, "CODEX_HOME=/fictional/changed-context")
			case "executable":
				_ = os.WriteFile(entry.CLI, []byte("changed fictional executable"), 0700)
			case "same_metadata_replacement":
				st, _ := os.Stat(entry.CLI)
				raw, _ := os.ReadFile(entry.CLI)
				tmp := entry.CLI + ".replacement"
				if err := os.WriteFile(tmp, raw, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(tmp, st.ModTime(), st.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(tmp, entry.CLI); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				_ = os.WriteFile(filepath.Join(dir, accounts.UsagePollingName), []byte(`{"schemaVersion":1,"entries":[],"unexpected":true}`), 0600)
			}
			accountUsagePollDue(context.Background(), dir, environment, func() time.Time { return at }, func(context.Context, string, string, []string, string) ([]byte, error) {
				t.Fatal("drift/corruption called provider")
				return nil, nil
			})
			if kind != "corrupt" {
				var out, errs bytes.Buffer
				code := accountsCommand(env{context.Background(), &out, &errs}, []string{"usage-polling", "--directory", dir, "--id", entry.ID, "--disable", "--json"}, nil)
				if code != exitOK || !strings.Contains(out.String(), `"state":"off"`) {
					t.Fatal("cannot revoke changed selection", code, errs.String(), out.String())
				}
			}
		})
	}
}
func TestAutomaticUsageSettingsRefuseInvalidAndRemainPrivate(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	var out, errs bytes.Buffer
	command := func(args ...string) int {
		out.Reset()
		errs.Reset()
		return accountsCommand(env{context.Background(), &out, &errs}, append([]string{"usage-polling", "--directory", dir, "--id", entry.ID}, args...), nil)
	}
	if command("--enable", "--json") != exitOK {
		t.Fatal(errs.String())
	}
	settingsBefore, _ := accounts.LoadUsagePolling(dir)
	if command("--enable", "--json") != exitOK {
		t.Fatal(errs.String())
	}
	settingsAfter, _ := accounts.LoadUsagePolling(dir)
	if settingsBefore.Entries[0].Grant != settingsAfter.Entries[0].Grant || !settingsBefore.Entries[0].NextAt.Equal(settingsAfter.Entries[0].NextAt) {
		t.Fatal("idempotent enable reset the grant or cadence")
	}
	if strings.Contains(out.String(), "grant") || strings.Contains(out.String(), "binding") || strings.Contains(out.String(), entry.CLI) {
		t.Fatal("private polling metadata disclosed")
	}
	st, err := os.Stat(filepath.Join(dir, accounts.UsagePollingName))
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		t.Fatal("settings not private", err)
	}
	if command("--enable", "--disable") != exitUsage {
		t.Fatal("ambiguous operation accepted")
	}
	if command("--disable") != exitOK {
		t.Fatal("disable", errs.String())
	}
	settings, err := accounts.LoadUsagePolling(dir)
	if err != nil || len(settings.Entries) != 0 {
		t.Fatal(settings, err)
	}
	// Missing settings produce no writes and do not opt a catalog into acquisition.
	_ = os.Remove(filepath.Join(dir, accounts.UsagePollingName))
	before, _ := os.ReadDir(dir)
	accountUsagePollDue(context.Background(), dir, os.Environ(), time.Now, func(context.Context, string, string, []string, string) ([]byte, error) {
		return nil, errors.New("must not be called")
	})
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("unconfigured read wrote settings")
	}
}

func TestAutomaticUsageProviderFreshnessAndBatchBudget(t *testing.T) {
	for _, provider := range []string{"codex", "claude-code", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			dir, entry := accountFixture(t, provider)
			at := dockNow
			enableUsageFixture(t, dir, entry, at)
			calls := 0
			run := func(ctx context.Context, p, cli string, env []string, wd string) ([]byte, error) {
				calls++
				if p != provider || cli != entry.CLI || wd != entry.Workdir {
					t.Fatal("wrong execution selection")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 12*time.Second {
					t.Fatal("unbounded provider/batch budget")
				}
				switch provider {
				case "claude-code":
					return []byte(claudeQuotaUsage), nil
				case "antigravity":
					return []byte(antigravityUsageFixture), nil
				default:
					return []byte(quotaResponse), nil
				}
			}
			accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
			if !panelQuotaProviderSupported(provider) {
				if calls != 0 {
					t.Fatal("unsupported platform called provider")
				}
				return
			}
			f, _ := accounts.Load(dir)
			q := accounts.QuotaViews(dir, f, os.Environ(), at)[entry.ID]
			if calls != 1 || q.State != "reported" {
				t.Fatal("provider not acquired", calls, q)
			}
			if provider != "codex" && (q.Freshness != "provider_unknown" || q.ObservedAt != nil || q.RetrievedAt == nil || !q.RetrievedAt.Equal(at)) {
				t.Fatal("retrieval fabricated provider freshness", q)
			}
		})
	}
	dir, _ := accountFixture(t, "codex")
	at := dockNow
	var entries []accounts.Entry
	if err := accounts.Update(dir, func(f *accounts.File) error {
		original := f.Entries[0]
		f.Entries = nil
		for i := 0; i < 4; i++ {
			e := accounts.NewEntry(*f, accounts.Entry{Label: "Example batch", Provider: "codex", Mode: original.Mode, CLI: original.CLI, Workdir: original.Workdir}, "", at, os.Environ())
			f.Entries = append(f.Entries, e)
			entries = append(entries, e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		enableUsageFixture(t, dir, e, at)
	}
	calls := 0
	run := func(context.Context, string, string, []string, string) ([]byte, error) {
		calls++
		return []byte(quotaResponse), nil
	}
	accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
	if calls != 2 {
		t.Fatal("batch cap not enforced", calls)
	}
	accountUsagePollDue(context.Background(), dir, os.Environ(), func() time.Time { return at }, run)
	if calls != 4 {
		t.Fatal("remaining due selections starved or already-read selections repeated", calls)
	}
}

func TestAutomaticUsageOfflineListAndControlReadsRemainSavedOnly(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	enableUsageFixture(t, dir, entry, time.Now().Add(-time.Minute))
	old := panelAccountsDir
	panelAccountsDir = dir
	t.Cleanup(func() { panelAccountsDir = old })
	d, _ := dockFixtureReader(t)
	var out, errs bytes.Buffer
	code := panelListCommand(env{context.Background(), &out, &errs}, []string{"--json", "--offline", "--directory", d.directory})
	if code != exitOK {
		t.Fatal(code, errs.String())
	}
	settings, _ := accounts.LoadUsagePolling(dir)
	if settings.Entries[0].AttemptAt != nil {
		t.Fatal("offline CLI list claimed a provider read")
	}
	// Control reviews use readers without the explicit UI acquisition capability.
	d.accountsDir = dir
	d.quotaRun = func(context.Context, string, string, []string, string) ([]byte, error) {
		t.Fatal("non-UI reader acquired usage")
		return nil, nil
	}
	d.read(context.Background(), false)
	settings, _ = accounts.LoadUsagePolling(dir)
	if settings.Entries[0].AttemptAt != nil {
		t.Fatal("non-UI reader claimed a provider read")
	}
}
