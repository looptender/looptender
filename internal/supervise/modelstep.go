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
// reference, with no text sent. The agent is told with its next message.
// When the window that blocked the stronger model has reset, the
// conversation is stepped back at a check that finds it between turns,
// still on the model it was stepped down to. It never acts on lanes, never
// changes a route or an account, and never runs on a dry run or for a held
// loop. At a one-model weekly limit it runs before any takeover.
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
	dir := t.loop.StateDir
	setting, err := state.LoadModelStepSetting(dir)
	if err != nil {
		t.warn("the model step-down setting could not be read: %v", err)
		return false, nil
	}
	rec, err := state.LoadModelStep(dir)
	if err != nil {
		t.warn("the model step-down record could not be read: %v", err)
		return false, nil
	}
	ms, canSet := a.(adapter.ModelSetter)
	if rec != nil && !t.now.Before(rec.ResetsAt) {
		if rec.Failed {
			// A failed step is tried again only in a later window.
			t.clearModelStep()
			rec = nil
		} else if obs.State == decide.Idle && canSet {
			return false, t.stepBack(ms, *rec, obs, setting)
		}
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
		if blocked != "" && state.ModelFamily(m) == blocked {
			i = n
			break
		}
	}
	if rec != nil && rec.StoodDown {
		rec = nil // a new limit starts a new step
	}
	switch {
	case rec != nil && rec.Failed:
		return false, nil // tried and failed in this window: wait for the reset
	case rec != nil && (i < 0 || setting.Models[i] != rec.To):
		return false, nil // the step in force is not what is blocked now
	case i < 0 || i+1 >= len(setting.Models):
		return false, nil // nothing weaker to step to: wait for the reset
	}
	next := setting.Models[i+1]
	step := state.ModelStep{From: setting.Models[i], To: next, Window: w.Key, ResetsAt: obs.ResetsAt.UTC(), At: t.now}
	if rec != nil {
		// A second step keeps the first: the conversation steps back to the
		// strongest model when its window resets.
		step.From, step.ResetsAt = rec.From, rec.ResetsAt
	}
	// The record first: a model changed with no record would never be
	// changed back.
	if err := state.SaveModelStep(dir, &step); err != nil {
		t.warn("the model step-down could not be recorded, so the model was not changed: %v", err)
		return false, nil
	}
	ctx, cancel := context.WithTimeout(t.ctx, min(modelStepTimeout, t.left()))
	err = ms.SetModel(ctx, next)
	cancel()
	if err != nil {
		step.Failed, step.Failure = true, "the model could not be changed"
		if serr := state.SaveModelStep(dir, &step); serr != nil {
			t.warn("the failed model step-down could not be recorded: %v", serr)
		}
		t.warn("model step-down to %s failed: %v; not tried again until the window resets", next, err)
		t.res.ModelStep = "step_failed"
		return false, nil // the usual limit handling goes on; no send was spent
	}
	t.res.ModelStep = "stepped_down"
	t.saveModelStepNote(fmt.Sprintf("[keepgoing:model-step to=%s from=%s resets=%s]\nlooptender notice: this conversation now runs on %s instead of %s, on the same account, because %s's weekly limit was reached. It moves back to %s after that limit resets at %s.",
		next, step.From, step.ResetsAt.Format(time.RFC3339), next, step.From, step.From, step.From, step.ResetsAt.Format(time.RFC3339)))
	// The send is reserved only now that the model has changed.
	if _, err := t.reserveSend(string(decide.Reprompt), target); err != nil {
		return true, err
	}
	ctx, cancel = context.WithTimeout(t.ctx, min(turnRetryTimeout, t.left()))
	_, err = r.RetryFailedTurn(ctx)
	cancel()
	t.res.Action, t.res.Recovery = string(decide.Reprompt), string(decide.RecoverByTurnRetry)
	t.res.Reason = fmt.Sprintf("%s reached; stepped down to %s on the same account, until it resets", w.Words(), next)
	var unsupported adapter.ErrUnsupported
	if errors.As(err, &unsupported) {
		// The next check finds the session in error on the weaker model and
		// recovers it as any error.
		return true, nil
	}
	return true, err
}

// stepBack changes the model back once the window has reset, only while the
// conversation's latest turn ran on the weaker model's family and the
// setting is on. Models are compared by weekly-limit family: the order may
// name a model as "sonnet" where BB reports "claude-sonnet-…".
//   - BB reported no model: it cannot be confirmed, so nothing changes and
//     the record is kept for a later check.
//   - On the stronger model's family already: the record is cleared.
//   - On another model, or step-down off: it stands down, leaving the model
//     as a person or the agent set it; the record says so, and the notice
//     says the conversation stays on that model.
func (t *tick) stepBack(ms adapter.ModelSetter, rec state.ModelStep, obs decide.Observation, setting *state.ModelStepSetting) error {
	now := state.ModelFamily(obs.TurnModel)
	if rec.StoodDown {
		if now != "" && now == state.ModelFamily(rec.From) {
			t.clearModelStep() // back on the stronger model after all
		}
		return nil
	}
	switch {
	case setting != nil && obs.TurnModel == "" && t.now.Sub(rec.ResetsAt) >= state.UnconfirmedFor:
		// A day past the reset and still no model reported: stop asking.
		rec.StoodDown, rec.StoodDownAt, rec.StoodDownWhy = true, t.now, "looptender could not confirm the conversation's model"
		if err := state.SaveModelStep(t.loop.StateDir, &rec); err != nil {
			t.warn("the model step-down record could not be saved: %v", err)
		}
		t.res.ModelStep = "stood_down"
		t.warn("model step-down: not moved back to %s: BB reported no model for the conversation for a day after the reset, so it could not be confirmed; it stays on the model it is on", rec.From)
		return nil
	case setting != nil && obs.TurnModel == "":
		t.res.ModelStep = "step_back_unconfirmed"
		// Said as a warning: the check's own decision follows and sets the reason.
		t.warn("model step-down: not moved back to %s yet: BB reports no model for the latest turn, so it cannot be confirmed that the conversation is still on %s", rec.From, rec.To)
		return nil
	case now != "" && now == state.ModelFamily(rec.From):
		t.clearModelStep() // already back, by a person or the agent
		return nil
	case setting == nil || now != state.ModelFamily(rec.To):
		rec.StoodDown, rec.StoodDownAt = true, t.now
		if err := state.SaveModelStep(t.loop.StateDir, &rec); err != nil {
			t.warn("the model step-down record could not be saved: %v", err)
		}
		t.res.ModelStep = "stood_down"
		t.warn("model step-down: not moved back to %s: the conversation is no longer on %s, or step-down is off; it stays on the model it is on", rec.From, rec.To)
		return nil
	}
	ctx, cancel := context.WithTimeout(t.ctx, min(modelStepTimeout, t.left()))
	err := ms.SetModel(ctx, rec.From)
	cancel()
	if err != nil {
		t.warn("stepping the model back to %s failed: %v", rec.From, err)
		return nil
	}
	t.clearModelStep()
	t.res.ModelStep = "stepped_back"
	t.saveModelStepNote(fmt.Sprintf("[keepgoing:model-step to=%s from=%s]\nlooptender notice: this conversation is back on %s: the weekly limit that moved it to %s has reset.", rec.From, rec.To, rec.From, rec.To))
	return nil
}

func (t *tick) clearModelStep() {
	if err := state.SaveModelStep(t.loop.StateDir, nil); err != nil {
		t.warn("the model step-down record could not be cleared: %v", err)
	}
}

func (t *tick) saveModelStepNote(text string) {
	if err := state.SaveModelStepNote(t.loop.StateDir, &state.ModelStepNote{Text: text, At: t.now}); err != nil {
		t.warn("the model change could not be kept for the agent's next message: %v", err)
	}
}

// withModelStepNote adds the pending model-change line to a message, and
// reports whether it did.
func (t *tick) withModelStepNote(text string) (string, bool) {
	n, err := state.LoadModelStepNote(t.loop.StateDir)
	if err != nil || n == nil || t.opts.DryRun {
		return text, false
	}
	return text + "\n\n" + n.Text + "\n", true
}

// modelStepNoteSent clears the line once a message carrying it was accepted.
func (t *tick) modelStepNoteSent(carried bool, err error) {
	if carried && err == nil {
		if serr := state.SaveModelStepNote(t.loop.StateDir, nil); serr != nil {
			t.warn("the model change line could not be cleared: %v", serr)
		}
	}
}
