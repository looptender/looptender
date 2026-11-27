package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func TestAutomaticUsageMenuOwnedPipeCancelsProviderAndReleasesOwnership(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	enableUsageFixture(t, dir, entry, time.Now())
	input, owner := io.Pipe()
	defer owner.Close()
	entered, canceled := make(chan struct{}), make(chan struct{})
	done := make(chan int, 1)
	go func() {
		done <- runAccountUsageOwnedWorker(context.Background(), dir, os.Environ(), input, time.Now, func(ctx context.Context, _ string, _ string, _ []string, _ string) ([]byte, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}, nil)
	}()
	if _, err := io.WriteString(owner, accountUsageOwnerHello); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("menu-only worker did not fetch with no panel or view")
	}
	owner.Close() // also models abrupt menu process death: the OS closes its pipe
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("owner EOF did not cancel provider")
	}
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("owned worker did not exit")
	}
	settings, _ := accounts.LoadUsagePolling(dir)
	if settings.SchedulerAt != nil {
		t.Fatal("stopped menu owner retained heartbeat")
	}
	lock, err := accounts.UsageSchedulerGuard(dir)
	if err != nil {
		t.Fatal("menu exit did not release ownership", err)
	}
	lock.Release()
	if settings.Entries[0].AttemptAt == nil {
		t.Fatal("menu did not reserve acquisition")
	}
}
func TestAutomaticUsageMenuOwnedWorkerRequiresGrantAndProtocol(t *testing.T) {
	for _, kind := range []string{"no_grant", "wrong_protocol", "revoked", "selection_changed"} {
		t.Run(kind, func(t *testing.T) {
			dir, entry := accountFixture(t, "codex")
			if kind != "no_grant" {
				enableUsageFixture(t, dir, entry, time.Now().Add(time.Hour))
			}
			input, owner := io.Pipe()
			defer owner.Close()
			ticks := make(chan time.Time)
			done := make(chan int, 1)
			var calls atomic.Int32
			go func() {
				done <- runAccountUsageOwnedWorker(context.Background(), dir, os.Environ(), input, time.Now, func(context.Context, string, string, []string, string) ([]byte, error) {
					calls.Add(1)
					return []byte(quotaResponse), nil
				}, ticks)
			}()
			if kind != "no_grant" {
				protocol := accountUsageOwnerHello
				if kind == "wrong_protocol" {
					protocol = strings.Repeat("x", len(protocol))
				}
				if _, err := io.WriteString(owner, protocol); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "revoked" || kind == "selection_changed" {
				waitUsage(t, func() bool { f, _ := accounts.LoadUsagePolling(dir); return f.SchedulerAt != nil })
				if kind == "revoked" {
					_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.Entries = []accounts.UsagePoll{}; return nil })
				} else {
					_ = accounts.Update(dir, func(f *accounts.File) error { f.Entries[0].Revision = strings.Repeat("e", 32); return nil })
				}
				ticks <- time.Now()
			}
			select {
			case code := <-done:
				want := exitOK
				if kind == "wrong_protocol" {
					want = exitFailed
				}
				if code != want {
					t.Fatal(code, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("invalid/unconfigured worker stayed alive")
			}
			if calls.Load() != 0 {
				t.Fatal("worker without valid due authorization called provider")
			}
		})
	}
}
func TestAutomaticUsageMenuEligibilityIncludesSelectionsBeyondNativeCap(t *testing.T) {
	r := dockReport{Accounts: &accounts.View{State: "available", Complete: true, Cards: []accounts.Card{}}}
	for i := 0; i < 17; i++ {
		r.Accounts.Cards = append(r.Accounts.Cards, accounts.Card{Provider: "codex", Label: "Example", UsageUpdating: &accounts.UsageUpdating{State: "off"}})
	}
	r.Accounts.Cards[16].UsageUpdating.State = "waiting"
	q := panelQuotaSummary(r)
	if !q.WorkerEligible || len(q.Accounts) != 16 || q.Omitted != 1 {
		t.Fatal("capped menu lost opt-in eligibility")
	}
	r.Accounts.Cards[16].UsageUpdating.State = "paused"
	if panelQuotaSummary(r).WorkerEligible {
		t.Fatal("changed selection authorized worker")
	}
}

func TestAutomaticUsageMenuTakesOverWhenPanelStopsWithoutViewReads(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	var clock atomic.Int64
	clock.Store(time.Now().UTC().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	enableUsageFixture(t, dir, entry, now())
	var calls atomic.Int32
	run := func(context.Context, string, string, []string, string) ([]byte, error) {
		calls.Add(1)
		return []byte(quotaResponse), nil
	}
	panelTicks := make(chan time.Time)
	stopPanel := startAccountUsageSchedulerWith(context.Background(), dir, false, now, run, panelTicks)
	defer stopPanel()
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
	input, owner := io.Pipe()
	defer owner.Close()
	menuTicks := make(chan time.Time)
	done := make(chan int, 1)
	go func() {
		done <- runAccountUsageOwnedWorker(context.Background(), dir, os.Environ(), input, now, run, menuTicks)
	}()
	if _, err := io.WriteString(owner, accountUsageOwnerHello); err != nil {
		t.Fatal(err)
	}
	stopPanel()
	clock.Add(int64(5 * time.Minute))
	menuTicks <- now()
	waitUsage(t, func() bool {
		f, _ := accounts.Load(dir)
		q := accounts.QuotaViews(dir, f, os.Environ(), now())[entry.ID]
		return q.ObservedAt != nil && q.ObservedAt.Equal(now())
	})
	if calls.Load() != 2 {
		t.Fatal("duplicate or missing acquisition across panel/menu ownership", calls.Load())
	}
	owner.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("menu worker did not stop")
	}
}

func TestAutomaticUsageMenuWorkerCommandHasNoImplicitAuthority(t *testing.T) {
	dir, entry := accountFixture(t, "codex")
	// Production command/runner, with a valid grant not due for an hour: no
	// stand-in provider can be launched while proving the actual lease protocol.
	enableUsageFixture(t, dir, entry, time.Now().Add(time.Hour))
	input, owner := io.Pipe()
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- accountsUsageWorkerCommand(env{ctx, io.Discard, io.Discard}, []string{"--directory", dir, "--owner-stdin"}, input)
	}()
	if _, err := io.WriteString(owner, accountUsageOwnerHello); err != nil {
		t.Fatal(err)
	}
	waitUsage(t, func() bool { f, _ := accounts.LoadUsagePolling(dir); return f.SchedulerAt != nil })
	owner.Close()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("command ignored lease closure")
	}
	settings, _ := accounts.LoadUsagePolling(dir)
	if settings.Entries[0].AttemptAt != nil {
		t.Fatal("command bypassed due cadence")
	}
	for _, args := range [][]string{{"--directory", dir}, {"--directory", dir, "--owner-stdin", "--enable"}} {
		input, owner := io.Pipe()
		code := accountsUsageWorkerCommand(env{context.Background(), io.Discard, io.Discard}, args, input)
		input.Close()
		owner.Close()
		if code != exitUsage {
			t.Fatal("worker flags granted implicit authority", code)
		}
	}
}
