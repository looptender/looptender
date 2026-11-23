package supervise

import (
	"context"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// steppingAdapter can change its model and retry a failed turn.
type steppingAdapter struct {
	*retryingAdapter
	models []string
}

func (s *steppingAdapter) SetModel(_ context.Context, m string) error {
	s.models = append(s.models, m)
	return nil
}

func steppingWorld(t *testing.T, obs decide.Observation) (*world, *steppingAdapter) {
	w := newWorld(t, true, false)
	f := &fakeAdapter{obs: obs}
	s := &steppingAdapter{retryingAdapter: &retryingAdapter{fakeAdapter: f}}
	w.adapters[state.Primary] = f
	return w, s
}

func stepTick(t *testing.T, w *world, s *steppingAdapter, now time.Time) Result {
	t.Helper()
	o := w.opts(now)
	o.NewAdapter = func(config.Agent, string, string) (adapter.Adapter, error) { return s, nil }
	res, err := Tick(context.Background(), w.cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func modelLimit(key string, reset time.Time) decide.Observation {
	o := limited
	o.ResetsAt = reset
	o.LimitWindow = decide.CleanLimitWindow(key, "", key)
	return o
}

// At one model's weekly limit, with step-down on, the conversation goes on
// with the next model of the order on the same account: its model is
// changed and its failed turn retried by reference, with no text sent. It
// steps back once that window has reset and the session is between turns.
func TestModelStepDownAndBack(t *testing.T) {
	reset := t0.Add(30 * time.Hour)
	w, s := steppingWorld(t, modelLimit("seven_day_opus", reset))
	if err := state.SaveModelStepSetting(w.dir, &state.ModelStepSetting{Models: []string{"claude-opus-example", "claude-sonnet-example"}, SetAt: t0}); err != nil {
		t.Fatal(err)
	}
	res := stepTick(t, w, s, t0)
	if res.ModelStep != "stepped_down" || res.Action != "reprompt" || len(s.models) != 1 || s.models[0] != "claude-sonnet-example" || s.retries != 1 || len(s.sent) != 0 {
		t.Fatalf("%+v models %v retries %d sent %q", res, s.models, s.retries, s.sent)
	}
	rec, err := state.LoadModelStep(w.dir)
	if err != nil || rec == nil || rec.From != "claude-opus-example" || rec.To != "claude-sonnet-example" || !rec.ResetsAt.Equal(reset) {
		t.Fatalf("%+v %v", rec, err)
	}
	// Running on the weaker model: nothing more.
	s.obs = decide.Observation{State: decide.Active}
	if res := stepTick(t, w, s, t0.Add(time.Hour)); res.ModelStep != "" || len(s.models) != 1 {
		t.Fatalf("%+v %v", res, s.models)
	}
	// Reset passed, but mid-turn: not yet.
	if res := stepTick(t, w, s, reset.Add(time.Minute)); res.ModelStep != "" || len(s.models) != 1 {
		t.Fatalf("stepped back mid-turn: %+v", res)
	}
	// Between turns: back to the stronger model.
	s.obs = decide.Observation{State: decide.Idle}
	if res := stepTick(t, w, s, reset.Add(2*time.Minute)); res.ModelStep != "stepped_back" || len(s.models) != 2 || s.models[1] != "claude-opus-example" {
		t.Fatalf("%+v %v", res, s.models)
	}
	if rec, _ := state.LoadModelStep(w.dir); rec != nil {
		t.Fatalf("record kept: %+v", rec)
	}
}

// Nothing is stepped for the all-models window, the 5-hour window, a model
// not in the order or the last one, a dry run, a held loop, or with the
// setting off.
func TestModelStepOnlyForAModelWindowItCanLeave(t *testing.T) {
	reset := t0.Add(30 * time.Hour)
	order := &state.ModelStepSetting{Models: []string{"claude-opus-example", "claude-sonnet-example"}, SetAt: t0}
	for name, c := range map[string]struct {
		obs     decide.Observation
		setting *state.ModelStepSetting
		dry     bool
		paused  bool
	}{
		"all models":    {modelLimit("seven_day", reset), order, false, false},
		"five hours":    {modelLimit("five_hour", reset), order, false, false},
		"not in order":  {modelLimit("seven_day_haiku", reset), order, false, false},
		"last in order": {modelLimit("seven_day_sonnet", reset), order, false, false},
		"setting off":   {modelLimit("seven_day_opus", reset), nil, false, false},
		"dry run":       {modelLimit("seven_day_opus", reset), order, true, false},
		"paused":        {modelLimit("seven_day_opus", reset), order, false, true},
	} {
		w, s := steppingWorld(t, c.obs)
		if err := state.SaveModelStepSetting(w.dir, c.setting); err != nil {
			t.Fatal(err)
		}
		if c.paused {
			if _, err := control.Update(w.dir, func(f *control.File) { f.Paused = true }); err != nil {
				t.Fatal(err)
			}
		}
		o := w.opts(t0)
		o.DryRun = c.dry
		o.NewAdapter = func(config.Agent, string, string) (adapter.Adapter, error) { return s, nil }
		res, err := Tick(context.Background(), w.cfg, o)
		if err != nil {
			t.Fatal(err)
		}
		if res.ModelStep != "" || len(s.models) != 0 || s.retries != 0 {
			t.Fatalf("%s: %+v %v", name, res, s.models)
		}
	}
}
