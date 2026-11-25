package supervise

import (
	"context"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/health"
	"github.com/looptender/looptender/internal/probe"
	"github.com/looptender/looptender/internal/state"
)

func TestQuotaSwitchThresholdAtTurnEnd(t *testing.T) {
	setup := func(t *testing.T, switchAt float64, usageAt time.Time, resetAt time.Time, used float64, identityOverride string) (*world, Result) {
		t.Helper()
		w := newWorld(t, true, true)
		w.cfg.Loop.Probe = config.Probe{Kind: "claude", Model: "example-small"}
		w.cfg.Loop.SwitchAt = switchAt
		w.primary(decide.Observation{State: decide.Idle})
		w.probeOK["claude-other"] = true
		w.probeOK["codex-main"] = true

		// Seed confirmed health for fallback and a usage reading for primary.
		reg := &health.Registry{Candidates: map[string]*health.Entry{}}
		reg.Record("claude-other", probe.Result{OK: true, Tier: probe.Scheduled, At: t0.Add(-5 * time.Minute)})
		if !usageAt.IsZero() {
			id := probe.UsageIdentity(w.cfg.Loop.PrimaryCandidate())
			if identityOverride != "" {
				id = identityOverride
			}
			reg.Record("primary", probe.Result{
				OK:            true,
				Tier:          probe.Scheduled,
				At:            usageAt,
				UsageIdentity: id,
				Usage: &probe.Usage{
					At:       usageAt,
					Source:   probe.UsageSourceClaude,
					Identity: id,
					Windows:  []probe.UsageWindow{{Key: "seven_day", Used: used, ResetsAt: &resetAt}},
				},
			})
		}
		if err := reg.Save(w.cfg.Loop.StateDir); err != nil {
			t.Fatal(err)
		}
		res, err := Tick(context.Background(), w.cfg, w.opts(t0))
		if err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		return w, res
	}

	t.Run("fresh reading at threshold switches to confirmed fallback", func(t *testing.T) {
		reset := t0.Add(24 * time.Hour)
		w, res := setup(t, 0.85, t0.Add(-10*time.Minute), reset, 0.88, "")
		if res.Action != string(decide.Handoff) || res.Handoff == nil || res.Handoff.To != "claude-other" || res.Handoff.Reason != "quota_threshold" {
			t.Fatalf("Tick() = %+v (handoff=%+v)", res, res.Handoff)
		}
		st := loadState(t, w)
		if st.ActiveTarget != "claude-other" || st.TargetSetBy != state.SetByHandoff || !st.PrimaryResetsAt.Equal(reset) {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("stale reading past TTL never switches", func(t *testing.T) {
		w, res := setup(t, 0.85, t0.Add(-7*time.Hour), t0.Add(24*time.Hour), 0.95, "")
		if res.Action != string(decide.Wake) {
			t.Fatalf("expected Wake on stale reading, got %+v", res)
		}
		if loadState(t, w).Target() != state.Primary {
			t.Fatal("expected primary target to remain active")
		}
	})

	t.Run("reading whose window already reset never switches", func(t *testing.T) {
		_, res := setup(t, 0.85, t0.Add(-10*time.Minute), t0.Add(-time.Minute), 0.95, "")
		if res.Action != string(decide.Wake) {
			t.Fatalf("expected Wake after window reset, got %+v", res)
		}
	})

	t.Run("mismatched usage identity never switches", func(t *testing.T) {
		_, res := setup(t, 0.85, t0.Add(-10*time.Minute), t0.Add(24*time.Hour), 0.95, "other-account-identity")
		if res.Action != string(decide.Wake) {
			t.Fatalf("expected Wake on mismatched identity, got %+v", res)
		}
	})

	t.Run("disabled switch_at never switches", func(t *testing.T) {
		_, res := setup(t, 0, t0.Add(-10*time.Minute), t0.Add(24*time.Hour), 0.99, "")
		if res.Action != string(decide.Wake) {
			t.Fatalf("expected Wake when switch_at is 0, got %+v", res)
		}
	})
}
