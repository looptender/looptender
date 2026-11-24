package supervise

import (
	"context"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/decide"
)

// At a usage limit on a route that states its account, the check asks for
// that account's login check; never on a dry run, never for a route that
// states none, and never when the target is not at a limit.
func TestLimitAsksForTheStatedAccountsLoginCheck(t *testing.T) {
	w := newWorld(t, true, false)
	w.cfg.Loop.Agent.Account = "example-claude-a"
	obs := limited
	obs.ResetsAt = t0.Add(5 * time.Hour)
	p := w.primary(obs)
	var asked []string
	tick := func(dry bool) {
		o := w.opts(t0)
		o.DryRun = dry
		o.AccountCheck = func(_ context.Context, id string) { asked = append(asked, id) }
		if _, err := Tick(context.Background(), w.cfg, o); err != nil {
			t.Fatal(err)
		}
	}
	tick(true)
	if len(asked) != 0 {
		t.Fatal("a dry run checked a login")
	}
	tick(false)
	if len(asked) != 1 || asked[0] != "example-claude-a" {
		t.Fatalf("%v", asked)
	}
	p.obs = decide.Observation{State: decide.Idle}
	tick(false)
	w.cfg.Loop.Agent.Account = ""
	p.obs = obs
	tick(false)
	if len(asked) != 1 {
		t.Fatalf("checked without a limit or a stated account: %v", asked)
	}
}
