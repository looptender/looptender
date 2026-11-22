package supervise

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// A BB thread cannot change provider. A child lane moved to a coordinator on
// another provider therefore stays on its own, and after a usage limit there
// it stays blocked until the limit resets. Replacing it means starting a
// successor thread on the coordinator's provider that continues it: one
// `bb thread spawn` with the coordinator as parent, in the child's project
// and environment, with an explicit model and permission mode, a title a
// person can recognise, and a fixed first prompt ending "Continue from
// @thread:<old child>." looptender reads nothing of the child's content, and
// the old child is not messaged, stopped, archived or re-parented.
//
// It is never automatic by default: a new_thread fallback opts in with
// bb.replace_blocked_children, and a person runs transfer replace-children.
// Creation uses the bb.new_thread rules: the intent, with a unique title, is
// saved and counted before the spawn; after any failure the title is looked
// for, and exactly one match is the thread; an outcome that cannot be proven
// is not retried and leaves the intent for a person. At most
// config.ChildSuccessorsPerRun per coordinator, and loop.limits.
// max_child_successors over the loop's life.

// ChildSuccessorMaker is the part of bb that replacing a child needs.
type ChildSuccessorMaker interface {
	ListChildren(ctx context.Context, parent string) ([]bb.ListedChild, error)
	InfoOf(ctx context.Context, thread string) (bb.ThreadInfo, error)
	// InspectLane gives a child's verified status and error class; the
	// maker is built for the coordinator thread, the lanes' parent.
	InspectLane(ctx context.Context, id string) decide.LaneFacts
	FindByTitle(ctx context.Context, p bb.Place, title string) ([]string, error)
	Spawn(ctx context.Context, r bb.SpawnRequest) (string, error)
}

// ReplacePlan is one run's request.
type ReplacePlan struct {
	Loop        string
	Coordinator string
	Provider    string // the coordinator's provider: successors run on it
	Model       string
	Reasoning   string
	Permission  string // the most a successor may have
	// UnknownChildPermission, when set, caps a successor whose old lane's
	// permission mode BB does not report. A successor never gets more than
	// the lane it replaces had, when that is known.
	UnknownChildPermission string
	// AllOldProvider replaces every live child on another provider, not
	// only those blocked on a usage limit. Only a person asks for it.
	AllOldProvider bool
	Lifetime       int // loop.limits.max_child_successors
	DryRun         bool
	// Reserve, when set, records each creation as a send against
	// loop.limits.max_sends before it is made; state.ErrSendLimit stops the
	// run with the rest counted in SendLimit.
	Reserve func() error
}

// ReplaceResult counts what a run found and did.
type ReplaceResult struct {
	Eligible    int  `json:"eligible"`
	WouldCreate int  `json:"wouldCreate"`
	Created     int  `json:"created"`
	Replaced    int  `json:"replaced"` // successors on record for this coordinator's lanes
	CappedRun   int  `json:"cappedPerRun,omitempty"`
	CappedLife  int  `json:"cappedLifetime,omitempty"`
	SendLimit   int  `json:"stoppedBySendLimit,omitempty"`
	Unknown     int  `json:"unknown,omitempty"` // children that could not be inspected
	Unsettled   bool `json:"unsettled,omitempty"`
	// Title names an unsettled creation so a person can look for it. It is
	// the title looptender generated, with no child content.
	Title string `json:"title,omitempty"`
	// Listed is false when the coordinator's children could not be listed.
	Listed bool `json:"listed"`
}

// childSuccessorAction is a successor's record in the send ledger. Like
// new_thread it is settled by its own intent in state.json.
const childSuccessorAction = "child_successor"

// ErrSuccessorUnsettled means a successor may have been created and could
// not be found: nothing else is created until a person settles it.
var ErrSuccessorUnsettled = errors.New("a successor thread may have been created and could not be found")

const (
	successorInspectTime = 10 * time.Second
	successorSpawnTime   = 30 * time.Second
	successorFindTime    = 20 * time.Second
)

func successorTitle(loop string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("keepgoing: %s lane (continued, %s)", loop, hex.EncodeToString(b)), nil
}

// successorPrompt is fixed text and the reference to the old child.
func successorPrompt(loop, child string) string {
	return "keepgoing started this thread for the \"" + loop + "\" loop to continue a child lane whose provider hit a usage limit. " +
		"A thread cannot change provider, so that lane cannot run here. " +
		"Read the earlier thread for its task, what it finished and what was left, before doing anything. " +
		"The earlier thread was left as it was: when its provider's limit resets it may run again, so check that it is not working before you redo its work, and say in this thread if it is. " +
		"Report to your parent thread as the earlier one would have.\n\nContinue from @thread:" + child + ".\n"
}

// ReplaceChildren runs one replacement for p.Coordinator. rec is the record
// in state, updated in place; lifetime is the loop's count, updated in place.
// env.Save is called after every change, before any further bb command.
func ReplaceChildren(ctx context.Context, maker ChildSuccessorMaker, rec **state.ChildSuccessors, lifetime *int, p ReplacePlan, env ChildMoveEnv) (ReplaceResult, error) {
	res := ReplaceResult{}
	r := *rec
	if r == nil || r.Coordinator != p.Coordinator {
		carried := map[string]state.ChildSuccessor{}
		var intent *state.ChildSuccessorIntent
		if r != nil {
			for k, v := range r.Replaced {
				carried[k] = v
			}
			intent = r.Intent // an unsettled creation is never forgotten
		}
		r = &state.ChildSuccessors{Coordinator: p.Coordinator, Provider: p.Provider, Replaced: carried, Intent: intent}
		*rec = r
		if !p.DryRun {
			env.Save()
		}
	}
	if r.Replaced == nil {
		r.Replaced = map[string]state.ChildSuccessor{}
	}
	// An earlier creation whose outcome was never recorded is settled first.
	if in := r.Intent; in != nil {
		if p.DryRun {
			res.Unsettled, res.Title = true, in.Title
			return res, ErrSuccessorUnsettled
		}
		fctx, cancel := context.WithTimeout(ctx, successorFindTime)
		ids, err := maker.FindByTitle(fctx, bb.Place{Project: in.Project, Environment: in.Environment}, in.Title)
		cancel()
		if err != nil || len(ids) != 1 {
			res.Unsettled, res.Title = true, in.Title
			return res, ErrSuccessorUnsettled
		}
		r.Replaced[in.Child] = state.ChildSuccessor{Thread: ids[0], Title: in.Title, At: time.Now().UTC()}
		r.Intent = nil
		env.Save()
	}
	lctx, cancel := context.WithTimeout(ctx, successorInspectTime)
	children, err := maker.ListChildren(lctx, p.Coordinator)
	cancel()
	if err != nil {
		env.Warn("replacing blocked child lanes: the coordinator's children could not be listed; nothing was created")
		return res, nil
	}
	res.Listed = true
	successors := map[string]bool{}
	for _, s := range r.Replaced {
		successors[s.Thread] = true
	}
	type candidate struct {
		id         string
		place      bb.Place
		permission string
	}
	var eligible []candidate
	ids := []string{}
	for _, c := range children {
		if !c.Archived && !successors[c.ID] {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, done := r.Replaced[id]; done {
			continue
		}
		if env.Left() < successorInspectTime {
			res.Unknown++
			continue
		}
		ictx, cancel := context.WithTimeout(ctx, successorInspectTime)
		info, err := maker.InfoOf(ictx, id)
		cancel()
		if err != nil {
			res.Unknown++
			continue
		}
		if info.Provider == p.Provider {
			continue // it can run under the coordinator's provider
		}
		ictx, cancel = context.WithTimeout(ctx, successorInspectTime)
		f := maker.InspectLane(ictx, id)
		cancel()
		if !f.Verified {
			res.Unknown++
			continue
		}
		// Never a lane that waits for a person, has queued input or a
		// provider retry on the way.
		if f.WaitingOnPerson || f.Queued > 0 || f.Retrying {
			continue
		}
		if !p.AllOldProvider && !(f.Errored && f.ClassKnown && f.Class == decide.ErrRateLimit) {
			continue
		}
		perm := p.Permission
		limit := info.Permission
		if limit == "" {
			limit = p.UnknownChildPermission
		}
		if config.PermissionRank(limit) >= 0 && config.PermissionRank(limit) < config.PermissionRank(perm) {
			perm = limit
		}
		eligible = append(eligible, candidate{id, info.Place, perm})
	}
	res.Eligible = len(eligible)
	res.Replaced = len(r.Replaced)
	if p.Lifetime <= 0 {
		p.Lifetime = config.DefaultMaxChildSuccessors
	}
	room := min(config.ChildSuccessorsPerRun-r.Created, p.Lifetime-*lifetime)
	room = max(room, 0)
	res.WouldCreate = min(len(eligible), room)
	if over := len(eligible) - res.WouldCreate; over > 0 {
		if config.ChildSuccessorsPerRun-r.Created <= p.Lifetime-*lifetime {
			res.CappedRun = over
		} else {
			res.CappedLife = over
		}
	}
	if p.DryRun {
		return res, nil
	}
	r.SkippedRun, r.SkippedLife = res.CappedRun, res.CappedLife
	env.Save()
	for _, c := range eligible[:res.WouldCreate] {
		if env.Left() < successorSpawnTime+successorFindTime || env.Held() {
			break
		}
		title, err := successorTitle(p.Loop)
		if err != nil {
			return res, errors.New("could not generate a title")
		}
		r.Intent = &state.ChildSuccessorIntent{Child: c.id, Title: title, Project: c.place.Project, Environment: c.place.Environment, RequestedAt: time.Now().UTC()}
		r.Created++
		*lifetime++
		env.Save()
		if p.Reserve != nil {
			if err := p.Reserve(); err != nil {
				// Nothing was asked of bb: the attempt is not counted.
				r.Intent = nil
				r.Created--
				*lifetime--
				env.Save()
				if errors.Is(err, state.ErrSendLimit) {
					res.SendLimit = res.WouldCreate - res.Created
					return res, nil
				}
				return res, err
			}
		}
		sctx, cancel := context.WithTimeout(ctx, successorSpawnTime)
		thread, err := maker.Spawn(sctx, bb.SpawnRequest{Place: c.place, Provider: p.Provider, Model: p.Model, Reasoning: p.Reasoning,
			Permission: c.permission, Title: title, Prompt: successorPrompt(p.Loop, c.id), Parent: p.Coordinator})
		cancel()
		if err != nil {
			r.Intent.NotStarted = errors.Is(err, bb.ErrSpawnNotStarted)
			fctx, cancel := context.WithTimeout(ctx, successorFindTime)
			found, ferr := maker.FindByTitle(fctx, c.place, title)
			cancel()
			switch {
			case ferr == nil && len(found) == 1:
				thread = found[0]
			case ferr == nil && len(found) == 0 && errors.Is(err, bb.ErrSpawnRefused):
				// Refused, and no such thread is listed: nothing was created.
				r.Intent = nil
				r.Created--
				*lifetime--
				env.Save()
				env.Warn(fmt.Sprintf("replacing a blocked child lane: bb refused to create its successor: %v", err))
				continue
			default:
				env.Save()
				res.Unsettled, res.Title = true, title
				return res, ErrSuccessorUnsettled
			}
		}
		r.Replaced[c.id] = state.ChildSuccessor{Thread: thread, Title: title, At: time.Now().UTC()}
		r.Intent = nil
		res.Created++
		res.Replaced = len(r.Replaced)
		env.Save()
	}
	return res, nil
}

// replaceBlockedChildren runs, past the holds, for a loop on a thread it
// created for a candidate that opted in with bb.replace_blocked_children,
// once that handoff's child move is done. held reports that a creation could
// not be settled and the loop was held for a person.
func (t *tick) replaceBlockedChildren() (held bool) {
	if t.fb == nil {
		return false
	}
	c, ok := t.fb.Find(t.st.Target())
	if !ok || c.Disabled || !isNewThread(c) || !c.BB.ReplacesChildren() {
		return false
	}
	coordinator := t.activeThread()
	if m := t.st.ChildMove; coordinator == "" || m == nil || m.To != coordinator || !m.Done {
		return false // the children are not all here yet
	}
	model, reasoning := c.BB.SuccessorModel()
	p := ReplacePlan{Loop: t.loop.Name, Coordinator: coordinator, Provider: c.Provider, Model: model, Reasoning: reasoning,
		Permission: c.BB.SuccessorPermission(), UnknownChildPermission: config.DefaultNewThreadPermission, Lifetime: t.loop.Limits.MaxChildSuccessors, DryRun: t.opts.DryRun,
		Reserve: func() error { _, err := t.reserveSend(childSuccessorAction, coordinator); return err }}
	save := func() {
		if saved, err := state.Load(t.loop.StateDir); err == nil {
			saved.ChildSuccessors, saved.ChildSuccessorsCreated = t.st.ChildSuccessors, t.st.ChildSuccessorsCreated
			_ = state.Save(t.loop.StateDir, saved)
		}
	}
	res, err := ReplaceChildren(t.ctx, t.opts.Successors(c.BB.CLI, coordinator), &t.st.ChildSuccessors, &t.st.ChildSuccessorsCreated, p,
		ChildMoveEnv{Left: t.left, Held: t.heldNow, Save: save, Warn: func(s string) { t.warn("%s", s) }})
	if res.Listed || res.Unsettled {
		t.res.Successors = &res
	}
	if errors.Is(err, ErrSuccessorUnsettled) && !t.opts.DryRun {
		d := decide.Decision{Action: decide.AwaitUser, Reason: fmt.Sprintf("keepgoing asked BB to create a successor thread titled %q for a blocked child lane and cannot tell whether it exists. "+
			"Nothing else was created. Look for it in BB; if it exists under another title, stop or archive it. Then resume to look again", res.Title)}
		t.res.Action, t.res.Reason = string(d.Action), d.Reason
		if err := t.act(nil, d); err != nil {
			t.warn("could not hold the loop: %v", err)
		}
		return true
	}
	if err != nil && !errors.Is(err, ErrSuccessorUnsettled) {
		t.warn("replacing blocked child lanes: %v", err)
	}
	if res.Created > 0 {
		t.report("handoff", fmt.Sprintf("%s: created %d successor thread(s) on %s for child lanes blocked on another provider's usage limit; the blocked lanes were left as they were", t.loop.Name, res.Created, c.Provider),
			map[string]any{"successorsCreated": res.Created, "successorsEligible": res.Eligible})
	}
	return false
}
