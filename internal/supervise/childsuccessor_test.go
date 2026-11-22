package supervise

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// fakeSuccessors stands in for bb when replacing blocked child lanes. It
// shares the children of a fakeThreads, so moved children are seen.
type fakeSuccessors struct {
	*fakeThreads
	provider   map[string]string
	permission map[string]string
	facts      map[string]decide.LaneFacts
	// spawnErr fails spawns; createdAnyway still creates the thread.
	spawnErr      error
	createdAnyway bool
	successors    []bb.SpawnRequest
}

func (s *fakeSuccessors) InfoOf(_ context.Context, id string) (bb.ThreadInfo, error) {
	p, ok := s.provider[id]
	if !ok {
		return bb.ThreadInfo{}, fmt.Errorf("bb thread show: failed")
	}
	return bb.ThreadInfo{Place: bb.Place{Project: "proj_example", Environment: "env_example"}, Provider: p, Permission: s.permission[id]}, nil
}

func (s *fakeSuccessors) InspectLane(_ context.Context, id string) decide.LaneFacts {
	return s.facts[id]
}

func (s *fakeSuccessors) Spawn(_ context.Context, r bb.SpawnRequest) (string, error) {
	s.successors = append(s.successors, r)
	id := fmt.Sprintf("thr_successor_%d", len(s.successors))
	if s.spawnErr == nil || s.createdAnyway {
		s.titles[r.Title] = append(s.titles[r.Title], id)
		s.children[r.Parent] = append(s.children[r.Parent], bb.ListedChild{ID: id})
		s.provider[id] = r.Provider
	}
	if s.spawnErr != nil {
		return "", s.spawnErr
	}
	return id, nil
}

var limitedLane = decide.LaneFacts{Verified: true, Errored: true, InputKnown: true, ClassKnown: true, Class: decide.ErrRateLimit}

// successorWorld: the primary (claude-code) hits its limit and hands off to
// a thread created on codex that opts in to replacing blocked lanes. The
// primary has lanes, some blocked on claude-code's limit.
func successorWorld(t *testing.T, lanes int, blocked int) (*world, *fakeThreads, *fakeSuccessors) {
	t.Helper()
	w, _, f := newThreadWorld(t)
	w.cfg.FallbackSet.Redundancy.MinHealthy = 1
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "claude-other" {
			c.Provider = "codex"
			c.BB.ChildModel, c.BB.ChildReasoning = "example-small-model", "low"
		}
	}
	s := &fakeSuccessors{fakeThreads: f, provider: map[string]string{}, facts: map[string]decide.LaneFacts{}}
	childrenOf(f, "thr_primary", lanes, 0)
	for i := 0; i < lanes; i++ {
		id := fmt.Sprintf("thr_child_%02d", i)
		s.provider[id] = "claude-code"
		s.facts[id] = decide.LaneFacts{Verified: true, InputKnown: true}
		if i < blocked {
			s.facts[id] = limitedLane
		}
	}
	return w, f, s
}

func successorTick(t *testing.T, w *world, f *fakeThreads, s *fakeSuccessors, at time.Time) Result {
	t.Helper()
	opts := w.opts(at)
	opts.Threads = func(string) ThreadCreator { return f }
	opts.Successors = func(_, coordinator string) ChildSuccessorMaker {
		if coordinator != "thr_created_1" {
			t.Fatalf("successors built for %s", coordinator)
		}
		return s
	}
	res, err := Tick(context.Background(), w.cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// After the handoff and the child move, the next check replaces the lanes
// blocked on the old provider's limit, and only those: one spawn each on the
// new provider, parented to the created thread, with the fixed prompt.
func TestBlockedLanesAreReplacedAfterAHandoff(t *testing.T) {
	w, f, s := successorWorld(t, 4, 2)
	successorTick(t, w, f, s, t0) // handoff and child move
	if len(s.successors) != 0 {
		t.Fatal("replaced in the handoff check")
	}
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	res := successorTick(t, w, f, s, t0.Add(time.Minute))
	if res.Successors == nil || res.Successors.Created != 2 || res.Successors.Eligible != 2 || len(s.successors) != 2 {
		t.Fatalf("%+v %d", res.Successors, len(s.successors))
	}
	for i, r := range s.successors {
		child := fmt.Sprintf("thr_child_%02d", i)
		if r.Provider != "codex" || r.Model != "example-small-model" || r.Reasoning != "low" || r.Permission != "accept-edits" || r.Parent != "thr_created_1" ||
			r.Project != "proj_example" || !strings.HasPrefix(r.Title, "keepgoing: example-loop lane (continued, ") || !strings.HasSuffix(r.Prompt, "Continue from @thread:"+child+".\n") {
			t.Fatalf("%+v", r)
		}
	}
	st, _ := state.Load(w.dir)
	if st.ChildSuccessorsCreated != 2 || !st.Replaced("thr_child_00") || !st.Replaced("thr_child_01") || st.Replaced("thr_child_02") || st.ChildSuccessors.Intent != nil {
		t.Fatalf("%+v %d", st.ChildSuccessors, st.ChildSuccessorsCreated)
	}
	// Nothing more on later checks, and the report says counts only.
	successorTick(t, w, f, s, t0.Add(2*time.Minute))
	if len(s.successors) != 2 {
		t.Fatal("replaced twice")
	}
	for _, e := range w.reports {
		if strings.Contains(e.Text, "thr_child") || strings.Contains(e.Text, "thr_successor") {
			t.Fatalf("report names a thread: %+v", e)
		}
	}
}

// Opted out, nothing is replaced; a dry run counts and creates none.
func TestBlockedLanesAreNotReplacedWhenOptedOutOrInADryRun(t *testing.T) {
	w, f, s := successorWorld(t, 3, 3)
	no := false
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "claude-other" {
			c.BB.ReplaceBlockedChildren = &no
		}
	}
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	if res := successorTick(t, w, f, s, t0.Add(time.Minute)); res.Successors != nil || len(s.successors) != 0 {
		t.Fatalf("%+v", res.Successors)
	}
	d, g, z := successorWorld(t, 3, 3)
	successorTick(t, d, g, z, t0)
	d.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	opts := d.opts(t0.Add(time.Minute))
	opts.DryRun = true
	opts.Threads = func(string) ThreadCreator { return g }
	opts.Successors = func(string, string) ChildSuccessorMaker { return z }
	res, _ := Tick(context.Background(), d.cfg, opts)
	if res.Successors == nil || res.Successors.WouldCreate != 3 || len(z.successors) != 0 {
		t.Fatalf("dry run: %+v", res.Successors)
	}
}

// At most config.ChildSuccessorsPerRun per coordinator, and the loop's
// lifetime limit; both are counted.
func TestSuccessorCaps(t *testing.T) {
	w, f, s := successorWorld(t, 8, 8)
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	res := successorTick(t, w, f, s, t0.Add(time.Minute))
	if res.Successors.Created != config.ChildSuccessorsPerRun || res.Successors.CappedRun != 3 {
		t.Fatalf("%+v", res.Successors)
	}
	successorTick(t, w, f, s, t0.Add(2*time.Minute))
	if len(s.successors) != config.ChildSuccessorsPerRun {
		t.Fatalf("over the per-handoff cap: %d", len(s.successors))
	}
	l, g, z := successorWorld(t, 4, 4)
	l.cfg.Loop.Limits.MaxChildSuccessors = 2
	successorTick(t, l, g, z, t0)
	l.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	if res := successorTick(t, l, g, z, t0.Add(time.Minute)); res.Successors.Created != 2 || res.Successors.CappedLife != 2 {
		t.Fatalf("lifetime: %+v", res.Successors)
	}
}

// A creation whose outcome cannot be proven holds the loop for a person
// and nothing else is created; resume looks again. A refusal proven by the
// listing is not counted.
func TestUnprovenSuccessorHoldsAndIsSettledByTitle(t *testing.T) {
	w, f, s := successorWorld(t, 3, 3)
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	s.spawnErr, s.createdAnyway = bb.ErrSpawnUncertain, false
	f.unlisted = true
	res := successorTick(t, w, f, s, t0.Add(time.Minute))
	c, _ := control.Load(w.dir)
	st, _ := state.Load(w.dir)
	if res.Action != "await_user" || !c.AwaitingUser || len(s.successors) != 1 || st.ChildSuccessors.Intent == nil || st.ChildSuccessorsCreated != 1 || !strings.Contains(res.Reason, "lane (continued, ") {
		t.Fatalf("%+v %+v", res, st.ChildSuccessors)
	}
	// Held: nothing more is asked.
	successorTick(t, w, f, s, t0.Add(2*time.Minute))
	if len(s.successors) != 1 {
		t.Fatal("created while held")
	}
	// The thread turns up under its title; resume settles it and carries on.
	f.titles[st.ChildSuccessors.Intent.Title] = []string{"thr_found"}
	s.spawnErr = nil
	control.Update(w.dir, func(c *control.File) { c.AwaitingUser = false })
	successorTick(t, w, f, s, t0.Add(3*time.Minute))
	st, _ = state.Load(w.dir)
	if st.ChildSuccessors.Replaced["thr_child_00"].Thread != "thr_found" || len(s.successors) != 3 || st.ChildSuccessorsCreated != 3 {
		t.Fatalf("%+v %d", st.ChildSuccessors, len(s.successors))
	}
	// A proven refusal is uncounted.
	r, g, z := successorWorld(t, 1, 1)
	successorTick(t, r, g, z, t0)
	r.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	z.spawnErr = bb.ErrSpawnRefused
	res = successorTick(t, r, g, z, t0.Add(time.Minute))
	st, _ = state.Load(r.dir)
	if res.Action == "await_user" || st.ChildSuccessorsCreated != 0 || st.ChildSuccessors.Intent != nil {
		t.Fatalf("%+v %+v", res, st.ChildSuccessors)
	}
}

// Only lanes on another provider that are blocked on a usage limit; never
// one waiting for a person, queued or retrying, or on the new provider.
func TestOnlyBlockedLanesOnTheOldProviderAreReplaced(t *testing.T) {
	w, f, s := successorWorld(t, 5, 5)
	waiting, queued, retrying := limitedLane, limitedLane, limitedLane
	waiting.WaitingOnPerson, queued.Queued, retrying.Retrying = true, 1, true
	s.facts["thr_child_01"], s.facts["thr_child_02"], s.facts["thr_child_03"] = waiting, queued, retrying
	s.provider["thr_child_04"] = "codex"
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	if res := successorTick(t, w, f, s, t0.Add(time.Minute)); res.Successors.Created != 1 || s.successors[0].Prompt == "" || !strings.HasSuffix(s.successors[0].Prompt, "thr_child_00.\n") {
		t.Fatalf("%+v", res.Successors)
	}
}

// A hold saved before the check: nothing is listed or created.
func TestHeldLoopCreatesNoSuccessor(t *testing.T) {
	w, f, s := successorWorld(t, 2, 2)
	successorTick(t, w, f, s, t0)
	control.Update(w.dir, func(c *control.File) { c.Paused = true })
	successorTick(t, w, f, s, t0.Add(time.Minute))
	if len(s.successors) != 0 {
		t.Fatal("created while paused")
	}
}

// Lane recovery leaves a replaced lane alone: retrying it would put two
// threads on the same work.
func TestLaneRecoveryLeavesAReplacedLaneAlone(t *testing.T) {
	w := newLaneWorld(t)
	ids := laneIDs(2)
	w.working(t, t0, ids...)
	st, _ := state.Load(w.dir)
	st.ChildSuccessors = &state.ChildSuccessors{Coordinator: "thr_primary", Provider: "claude-code", Replaced: map[string]state.ChildSuccessor{ids[0]: {Thread: "thr_successor_x", Title: "keepgoing: example-loop lane (continued, 00000000)", At: t0}}}
	state.Save(w.dir, st)
	w.failed(ids...)
	res := w.tick(t, t0.Add(time.Minute))
	if res.Lanes == nil || res.Lanes.NotRetried[laneReplaced] != 1 || len(w.a.retried) != 1 || w.a.retried[0] != ids[1] {
		t.Fatalf("%+v retried=%v", res.Lanes, w.a.retried)
	}
}

// noSuccessors is bb with no child lanes, for worlds that do not test
// successors: the real bb is never run from a test.
type noSuccessors struct{}

func (noSuccessors) ListChildren(context.Context, string) ([]bb.ListedChild, error) {
	return []bb.ListedChild{}, nil
}
func (noSuccessors) InfoOf(context.Context, string) (bb.ThreadInfo, error) {
	return bb.ThreadInfo{}, fmt.Errorf("no thread")
}
func (noSuccessors) InspectLane(context.Context, string) decide.LaneFacts { return decide.LaneFacts{} }
func (noSuccessors) FindByTitle(context.Context, bb.Place, string) ([]string, error) {
	return nil, nil
}
func (noSuccessors) Spawn(context.Context, bb.SpawnRequest) (string, error) {
	return "", fmt.Errorf("no spawn in this world")
}

// On by default with the candidate's own model when no child model is set;
// a successor never gets more permission than the lane it replaces had, and
// accept-edits when that is not reported. The created thread's first prompt
// says what will happen to blocked and idle lanes.
func TestSuccessorDefaultsAndPermission(t *testing.T) {
	w, f, s := successorWorld(t, 3, 3)
	obs := limited
	obs.HealthyChildren = []string{"thr_child_09"}
	obs.ErroredChildren = []string{"thr_child_00", "thr_child_01", "thr_child_02"}
	w.adapters[state.Primary].obs = obs
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "claude-other" {
			c.BB.ChildModel, c.BB.ChildReasoning, c.BB.ChildPermissionMode = "", "", "full"
		}
	}
	s.permission = map[string]string{"thr_child_00": "auto", "thr_child_01": "full"}
	successorTick(t, w, f, s, t0)
	prompt := f.spawned[0].Prompt
	if !strings.Contains(prompt, "3 child thread(s) were in error, so up to 3 successor lane(s) will be started and 0 blocked child thread(s) skipped at the cap of 5 per handoff") || !strings.Contains(prompt, "The 1 idle child thread(s)") || !strings.Contains(prompt, "BB may run a replaced child again") {
		t.Fatalf("first prompt: %q", prompt)
	}
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	successorTick(t, w, f, s, t0.Add(time.Minute))
	if len(s.successors) != 3 {
		t.Fatalf("%d", len(s.successors))
	}
	want := []string{"auto", "full", "accept-edits"}
	for i, r := range s.successors {
		if r.Model != "example-model" || r.Reasoning != "high" || r.Permission != want[i] {
			t.Fatalf("%d: %+v", i, r)
		}
	}
}

// status-facing counts: lanes skipped at a cap are recorded.
func TestSuccessorSkipsAtTheCapAreRecorded(t *testing.T) {
	w, f, s := successorWorld(t, 7, 7)
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	successorTick(t, w, f, s, t0.Add(time.Minute))
	st, _ := state.Load(w.dir)
	if st.ChildSuccessors.SkippedRun != 2 || st.ChildSuccessors.SkippedLife != 0 || len(st.ChildSuccessors.Replaced) != 5 {
		t.Fatalf("%+v", st.ChildSuccessors)
	}
}

// Unless configured, successors run with accept-edits, whatever the
// candidate's own permission mode.
func TestSuccessorPermissionDefaultsToAcceptEdits(t *testing.T) {
	w, f, s := successorWorld(t, 1, 1)
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "claude-other" {
			c.BB.PermissionMode = "full"
		}
	}
	s.permission = map[string]string{"thr_child_00": "full"}
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	successorTick(t, w, f, s, t0.Add(time.Minute))
	if len(s.successors) != 1 || s.successors[0].Permission != "accept-edits" {
		t.Fatalf("%+v", s.successors)
	}
}

// Each successor is a send against loop.limits.max_sends; at the limit the
// rest are left, uncounted, and said so.
func TestSuccessorsCountAgainstMaxSends(t *testing.T) {
	w, f, s := successorWorld(t, 3, 3)
	w.cfg.Loop.Limits.MaxSends = 2 // the handoff's own thread takes one
	successorTick(t, w, f, s, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	res := successorTick(t, w, f, s, t0.Add(time.Minute))
	st, _ := state.Load(w.dir)
	if len(s.successors) != 1 || res.Successors == nil || res.Successors.Created != 1 || res.Successors.SendLimit != 2 || st.ChildSuccessorsCreated != 1 || st.ChildSuccessors.Intent != nil {
		t.Fatalf("%+v %+v", res.Successors, st.ChildSuccessors)
	}
	sendsUsed(t, w, 2)
}
