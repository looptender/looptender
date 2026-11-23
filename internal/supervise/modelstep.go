package supervise

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

const modelStepTimeout = 20 * time.Second

// modelStep is the opt-in model step-down (looptender model-step). When the
// loop's own conversation is at a usage limit whose window is one model's
// (Claude's seven_day_<model>), the all-models window is not the one that
// blocked it, so the same account can go on with the next model of the
// configured order: the conversation's model is changed (bb thread update
// --model, same account, same settings) and its failed turn retried by
// reference, with no text sent. When the window that blocked the stronger
// model has reset, the conversation is stepped back to it at a check that
// finds it between turns. It never acts on lanes, never changes a route or
// an account, and never runs on a dry run or for a held loop.
//
// It reports whether it handled the check (a step-down was made or tried),
// in which case no other decision is made this check.
func (t *tick) modelStep(a adapter.Adapter, target string, obs decide.Observation, inWindow bool) (bool, error) {
	if t.opts.DryRun || target != state.Primary {
		return false, nil
	}
	if c := t.ctl; !c.Enabled || c.SupersededBy != "" || c.Paused || c.Stop || c.AwaitingUser || c.Completed {
		return false, nil
	}
	setting, err := state.LoadModelStepSetting(t.loop.StateDir)
	if err != nil {
		t.warn("the model step-down setting could not be read: %v", err)
		return false, nil
	}
	rec, err := state.LoadModelStep(t.loop.StateDir)
	if err != nil {
		t.warn("the model step-down record could not be read: %v", err)
		return false, nil
	}
	ms, canSet := a.(adapter.ModelSetter)
	// Step back: the stronger model's window has reset and the session is
	// between turns. Done even if the setting was turned off since.
	if rec != nil && !t.now.Before(rec.ResetsAt) && obs.State == decide.Idle && canSet {
		ctx, cancel := context.WithTimeout(t.ctx, min(modelStepTimeout, t.left()))
		err := ms.SetModel(ctx, rec.From)
		cancel()
		if err != nil {
			t.warn("stepping the model back to %s failed: %v", rec.From, err)
			return false, nil
		}
		if err := state.SaveModelStep(t.loop.StateDir, nil); err != nil {
			t.warn("the model step-down record could not be cleared: %v", err)
		}
		t.res.ModelStep = "stepped_back"
		return false, nil
	}
	if setting == nil || !canSet || !inWindow {
		return false, nil
	}
	w := obs.LimitWindow
	if obs.State != decide.Error || obs.ErrorClass != decide.ErrRateLimit || !obs.ResetsAt.After(t.now) || w.Kind() != "weekly_model" {
		return false, nil
	}
	r, canRetry := a.(adapter.TurnRetrier)
	if !canRetry {
		return false, nil
	}
	blocked := strings.ToLower(w.Model())
	i := -1
	for n, m := range setting.Models {
		if blocked != "" && strings.Contains(strings.ToLower(m), blocked) {
			i = n
			break
		}
	}
	if rec != nil && (i < 0 || setting.Models[i] != rec.To) {
		return false, nil // the step in force is not what is blocked now
	}
	if i < 0 || i+1 >= len(setting.Models) {
		return false, nil // nothing weaker to step to: wait for the reset
	}
	next := setting.Models[i+1]
	if _, err := t.reserveSend(string(decide.Reprompt), target); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(t.ctx, min(modelStepTimeout, t.left()))
	err = ms.SetModel(ctx, next)
	cancel()
	if err != nil {
		t.res.Action, t.res.Reason = string(decide.None), "model step-down failed: the model could not be changed"
		return true, err
	}
	step := state.ModelStep{From: setting.Models[i], To: next, Window: w.Key, ResetsAt: obs.ResetsAt.UTC(), At: t.now}
	if rec != nil {
		// A second step keeps the first: the conversation steps back to the
		// strongest model when its window resets.
		step.From, step.ResetsAt = rec.From, rec.ResetsAt
	}
	if err := state.SaveModelStep(t.loop.StateDir, &step); err != nil {
		t.warn("the model step-down could not be recorded: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.ctx, min(turnRetryTimeout, t.left()))
	res, err := r.RetryFailedTurn(ctx)
	cancel()
	t.res.Action, t.res.Recovery, t.res.ModelStep = string(decide.Reprompt), string(decide.RecoverByTurnRetry), "stepped_down"
	t.res.Reason = fmt.Sprintf("%s reached; stepped down to %s on the same account, until it resets", w.Words(), next)
	var unsupported adapter.ErrUnsupported
	if errors.As(err, &unsupported) {
		// The next check finds the session in error on the weaker model and
		// recovers it as any error.
		return true, nil
	}
	if err != nil {
		return true, err
	}
	_ = res
	return true, nil
}
