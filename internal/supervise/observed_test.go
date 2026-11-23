package supervise

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
)

// A loop on an observed sessionpipe target, with no prompt, runs its checks
// and never sends to or stops the session, whatever the session's state.
func TestObservedTargetNeverSendsOrStops(t *testing.T) {
	for _, tc := range []struct {
		name string
		obs  decide.Observation
		stop bool
		want string
	}{
		{"idle", decide.Observation{State: decide.Idle}, false, "between turns"},
		{"errored", decide.Observation{State: decide.Error, ErrorClass: decide.ErrUnknown}, false, "ended in error"},
		{"rate limited", decide.Observation{State: decide.Error, ErrorClass: decide.ErrRateLimit}, false, "ended in error"},
		{"waiting on a person", decide.Observation{State: decide.Active, AwaitingInput: true}, false, "waiting on a person"},
		{"stop asked while active", decide.Observation{State: decide.Active}, true, "never stopped by looptender"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true, false)
			w.cfg.Loop.Prompt = ""
			w.cfg.Loop.Agent = config.Agent{Adapter: config.SessionpipeAdapter, Provider: config.SessionpipeAdapter, Group: config.SessionpipeAdapter,
				Sessionpipe: &config.Sessionpipe{Session: "claude-code:example", EventsFile: "/example/events.jsonl"}}
			if tc.stop {
				if _, err := control.Update(w.dir, func(f *control.File) { f.Stop = true }); err != nil {
					t.Fatal(err)
				}
			}
			p := w.primary(tc.obs)
			for i := range 4 {
				res, err := Tick(context.Background(), w.cfg, w.opts(t0.Add(time.Duration(i)*3*time.Hour)))
				if err != nil {
					t.Fatalf("check %d: %v", i, err)
				}
				if res.Action != string(decide.None) || !strings.Contains(res.Reason, tc.want) {
					t.Fatalf("check %d: %s (%q), want none saying %q", i, res.Action, res.Reason, tc.want)
				}
			}
			if len(p.sent) != 0 || p.stopped != 0 || p.observed == 0 {
				t.Fatalf("sent %d, stopped %d, observed %d", len(p.sent), p.stopped, p.observed)
			}
		})
	}
	// A gone session holds for a person, as any gone target does.
	w := newWorld(t, true, false)
	w.cfg.Loop.Prompt = ""
	w.cfg.Loop.Agent = config.Agent{Adapter: config.SessionpipeAdapter, Sessionpipe: &config.Sessionpipe{Session: "codex:example", EventsFile: "/example/events.jsonl"}}
	p := w.primary(decide.Observation{State: decide.Idle, Gone: true})
	if res := runTick(t, w, t0); res.Action != string(decide.AwaitUser) || len(p.sent) != 0 {
		t.Fatalf("gone: %+v", res)
	}
}
