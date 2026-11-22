package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Round two of the takeover scenarios: the incident on a loop armed by
// `fallbacks arm`, where the takeover also starts successor lanes on the new
// provider for the coordinator's children blocked on the old one's limit
// (#428), together with the send allowance, holds, crashes, a full disk, a
// manual transfer and a build installed mid-way.

const (
	childModel     = "example-small-model"
	childReasoning = "low"
	idleLane       = "thr_example_idle"
	askingLane     = "thr_example_asking"
	codexLane      = "thr_example_codexlane"
)

// armed is the shape of an armed incident.
type armed struct {
	blocked    int // children blocked on the old provider's limit
	maxSends   int
	lifetime   int // loop.limits.max_child_successors; 0 leaves the default
	switchBack bool
}

// blockedLane names the i-th blocked child (from 1).
func blockedLane(i int) string { return fmt.Sprintf("thr_example_blocked%d", i) }

// armIncident sets up a single-route loop, arms it with `fallbacks arm` from
// its owner, gives the takeover a child model, wakes the coordinator once,
// gives it its children and puts it at a 132-hour limit. It returns the
// children and the reset.
func armIncident(w *World, a armed) (children []string, reset time.Time) {
	w.t.Helper()
	codexUsable(w)
	w.InitWith("--new-thread-provider", "none")
	w.EditConfig(func(l map[string]any) {
		l["report"] = map[string]any{"events": incidentEvents, "timeout": "5s"}
		limits, _ := l["limits"].(map[string]any)
		if limits == nil {
			limits = map[string]any{}
		}
		if a.maxSends > 0 {
			limits["max_sends"] = a.maxSends
		}
		if a.lifetime > 0 {
			limits["max_child_successors"] = a.lifetime
		}
		l["limits"] = limits
	})
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", all(exit(s, 0), ownerIs(w, primaryThread, 1)))
	r := w.RunAs(primaryThread, "fallbacks", "arm", "-c", w.Config, "--yes")
	if r.Code != 0 {
		w.t.Fatalf("fallbacks arm: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	w.initCalls()
	w.editFallback(initCandidate, func(bb map[string]any) {
		bb["child_model"], bb["child_reasoning"] = childModel, childReasoning
	})
	if a.switchBack {
		w.EditConfig(func(l map[string]any) {
			l["switch_back"] = map[string]any{"enabled": true, "min_after": "1h", "retry_every": "30m"}
			l["probe"] = map[string]any{"cli": w.BinPath("claude")}
		})
	}
	w.Advance(time.Minute)
	s = w.Tick()
	w.Check(s, "first wake", all(action(s, "wake"), sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"})))

	// The coordinator's children: some blocked on its provider's limit, one
	// idle, one waiting for a person, one on the new provider.
	w.Advance(5 * time.Minute)
	reset = w.Now.Add(incidentReset)
	primary := w.Thread(primaryThread)
	for i := 1; i <= a.blocked; i++ {
		c := primary.Child(blockedLane(i))
		c.Provider, c.Permission = "claude-code", "auto"
		c.StartTurn()
		c.RateLimited(reset)
		children = append(children, c.ID)
	}
	idle := primary.Child(idleLane)
	idle.Provider = "claude-code"
	idle.FinishTurn()
	asking := primary.Child(askingLane)
	asking.Provider = "claude-code"
	asking.StartTurn()
	w.writeJSON(askingLane+".interactions", []map[string]any{{"id": "pint_example2", "threadId": askingLane, "status": "pending"}})
	cx := primary.Child(codexLane)
	cx.Provider = "codex"
	cx.StartTurn()
	children = append(children, idleLane, askingLane, codexLane)
	w.Advance(5 * time.Minute)
	primary.RateLimited(reset)
	w.Advance(time.Minute)
	// A child's question holds the loop for a person before anything else,
	// the coordinator's limit included (documented: verified pending child
	// input requires an awaiting-user hold). The person answers it, so the
	// child works on, and resumes.
	s = w.Tick()
	w.Check(s, "held for the child's question", all(action(s, "await_user"), reasonHas(s, askingLane), nothingSent(s), spawns(w, 0)))
	w.writeJSON(askingLane+".interactions", []map[string]any{{"id": "pint_example2", "threadId": askingLane, "status": "resolved"}})
	s = w.Control("resume")
	w.Check(s, "a person resumes", exit(s, 0))
	w.Advance(time.Minute)
	return children, reset
}

// editFallback rewrites one candidate's bb section in the fallbacks file.
func (w *World) editFallback(id string, edit func(bb map[string]any)) {
	w.t.Helper()
	var cfg struct {
		Loop map[string]any `yaml:"loop"`
	}
	if err := yaml.Unmarshal([]byte(readFile(w, w.Config)), &cfg); err != nil {
		w.t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(w.Config), cfg.Loop["fallbacks"].(string))
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(readFile(w, path)), &doc); err != nil {
		w.t.Fatal(err)
	}
	for _, c := range doc["candidates"].([]any) {
		c := c.(map[string]any)
		if c["id"] == id {
			bb, _ := c["bb"].(map[string]any)
			edit(bb)
		}
	}
	b, err := yaml.Marshal(doc)
	if err == nil {
		err = os.WriteFile(path, b, 0o600)
	}
	if err != nil {
		w.t.Fatal(err)
	}
}

// successorSpawns lists the successor lanes created over the journey.
func successorSpawns(w *World) []Send {
	var out []Send
	for _, st := range w.Journal {
		for _, m := range st.Sent {
			if m.Kind == "spawn" && m.Parent != "" {
				out = append(out, m)
			}
		}
	}
	return out
}

// successorsFor are the old lanes the successor lanes continue, sorted.
func successorsFor(w *World) []string {
	var got []string
	for _, m := range successorSpawns(w) {
		if i := strings.Index(m.Message, "Continue from @thread:"); i >= 0 {
			got = append(got, strings.TrimSuffix(strings.TrimSpace(m.Message[i+len("Continue from @thread:"):]), "."))
		}
	}
	sort.Strings(got)
	return got
}

// replacedExactly: the successor lanes continue exactly these lanes, once
// each, all under the created thread on codex with the child model.
func replacedExactly(w *World, lanes ...string) error {
	want := append([]string{}, lanes...)
	sort.Strings(want)
	if got := successorsFor(w); fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("successor lanes continue %v, want %v", got, want)
	}
	for _, m := range successorSpawns(w) {
		if m.Parent != successor || m.Provider != "codex" || m.Model != childModel || m.Reasoning != childReasoning || m.Mode != "accept-edits" ||
			!strings.HasPrefix(m.Title, "keepgoing: example-loop lane (continued, ") {
			return fmt.Errorf("successor %s: parent %s provider %s model %s reasoning %s mode %s title %q", m.Thread, m.Parent, m.Provider, m.Model, m.Reasoning, m.Mode, m.Title)
		}
	}
	return nil
}

// successorStatus reads childSuccessors and sendAllowance from status.
type successorCounts struct {
	ChildSuccessors *struct {
		Replaced, LifetimeCreated, LifetimeMax, SkippedPerRun, SkippedLifetime int
		Pending                                                                bool
	} `json:"childSuccessors"`
	SendAllowance *struct {
		Max  int64
		Used *int64
	} `json:"sendAllowance"`
}

func countsOf(s *Step) successorCounts {
	var c successorCounts
	json.Unmarshal([]byte(s.Status.Raw), &c)
	return c
}

func successorsAre(s *Step, replaced, lifetime int) error {
	c := countsOf(s).ChildSuccessors
	if c == nil {
		return fmt.Errorf("status has no childSuccessors")
	}
	return check(c.Replaced == replaced && c.LifetimeCreated == lifetime && !c.Pending, "childSuccessors %+v, want replaced %d lifetime %d", *c, replaced, lifetime)
}

func sendsUsed(s *Step, want int64) error {
	a := countsOf(s).SendAllowance
	if a == nil || a.Used == nil {
		return fmt.Errorf("status has no send allowance used")
	}
	return check(*a.Used == want, "sendAllowance.used %d, want %d", *a.Used, want)
}

// noSendsTo: none of these threads was ever sent anything.
func noSendsTo(w *World, ids ...string) error {
	for _, id := range ids {
		if err := journeySends(w, id); err != nil {
			return err
		}
	}
	return nil
}

// TestScenarioArmedIncidentReplacesBlockedLanes (H): the incident on a loop
// armed by `fallbacks arm`. The created thread owns the loop and takes the
// five children; in the next check exactly the two lanes blocked on the old
// provider's limit get successor lanes, under the created thread, on codex,
// with the child model; the idle lane, the one waiting for a person and the
// one already on codex are left alone; every creation is a counted send.
func TestScenarioArmedIncidentReplacesBlockedLanes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	children, _ := armIncident(w, armed{blocked: 2, maxSends: 20})
	s := w.Tick()
	w.Check(s, "handoff", all(
		action(s, "handoff"), spawns(w, 1), targetIs(s, initCandidate, successor), ownerIs(w, successor, 2),
		childrenOf(w, successor, children...), childrenOf(w, primaryThread), noSendsTo(w, children...),
	))
	if len(s.Sent) == 1 {
		m := s.Sent[0].Message
		w.Check(s, "the first prompt's counts", all(
			check(strings.Contains(m, "At the last check it saw 5 direct child thread(s), 2 of them in error."), "child counts"),
			check(strings.Contains(m, "2 child thread(s) were in error, so up to 2 successor lane(s) will be started and 0 blocked child thread(s) skipped at the cap of 5 per handoff"), "successor counts"),
		))
	}
	w.Advance(5 * time.Minute)
	s = w.Tick()
	w.Check(s, "successor lanes", all(
		replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 2),
		sendsUsed(s, 4), noSendsTo(w, children...), ownerIs(w, successor, 2), reported(s, "keepgoing.handoff"),
	))
	for i := 0; i < 2; i++ {
		w.Advance(time.Hour)
		s = w.Tick()
		w.Check(s, "no more successors", all(check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w))), successorsAre(s, 2, 2), sendsUsed(s, 4)))
	}
}

// handedOff runs the handoff check of an armed incident.
func handedOff(w *World) {
	w.t.Helper()
	s := w.Tick()
	w.Check(s, "handoff", all(action(s, "handoff"), spawns(w, 1), ownerIs(w, successor, 2)))
	w.Advance(5 * time.Minute)
}

// tickWithHold runs one check that is held at its nth `thread spawn` while a person runs the hold command, then
// lets it go on.
func (w *World) tickWithHold(n int, hold string) (*Step, *Step) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.world, "hold.spawn"), []byte(fmt.Sprint(n)), 0o600); err != nil {
		w.t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() { done <- w.Run("tick", "-c", w.Config, "--now", w.stamp()) }()
	held := filepath.Join(w.world, "spawn.held")
	for deadline := time.Now().Add(30 * time.Second); !fileExists(held); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatal("the check never reached the held spawn")
		}
	}
	r := w.Run(hold, "-c", w.Config, "--note", "Fictional hold by a person")
	os.WriteFile(filepath.Join(w.world, "spawn.release"), nil, 0o600)
	tr := <-done
	os.Remove(held)
	os.Remove(filepath.Join(w.world, "spawn.release"))
	// The hold landed while the check ran: record the check first.
	var line map[string]any
	json.Unmarshal([]byte(strings.TrimSpace(tr.Stdout)), &line)
	s := w.record("tick (held mid-pass)", tr, line)
	h := w.record(hold+" (during the check)", r, nil)
	return s, h
}

// TestScenarioArmedIncidentSuccessorLimits (I): the successor pass under a
// crash at each external step, the per-handoff cap with seven blocked
// lanes, the send allowance reached mid-way, the lifetime cap, and a hold
// written between two creations. No lane gets two successors and none is
// created beyond a cap.
func TestScenarioArmedIncidentSuccessorLimits(t *testing.T) {
	t.Parallel()
	t.Run("killed after a successor was created, before it was recorded", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		handedOff(w)
		w.killAfter("spawn")
		s := w.killedTick("killed after the first successor spawn")
		w.Check(s, "one successor asked for", check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))))
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "found by its title, the other created", all(replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 2), holds(s), noSendsTo(w, children...)))
	})
	t.Run("a stop with a successor created and not recorded", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		armIncident(w, armed{blocked: 2})
		handedOff(w)
		w.killAfter("spawn")
		w.killedTick("killed after the first successor spawn")
		created := successorSpawns(w)[0].Thread
		w.Advance(time.Minute)
		s := w.Control("stop", "--note", "Fictional stop by a person")
		w.Check(s, "stop saved", exit(s, 0))
		w.Advance(time.Minute)
		s = w.Tick()
		// The unrecorded successor may be running a lane's work: stop wins,
		// as for an unrecorded created thread.
		w.Check(s, "the stop reaches the created thread and the unrecorded successor", all(
			ranBB(s, "thread", "stop", successor, "--json"), ranBB(s, "thread", "stop", created, "--json"),
			check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))),
		))
	})
	t.Run("killed before bb was asked, with the request already marked", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		handedOff(w)
		w.killBefore("spawn")
		s := w.killedTick("killed before the first successor spawn")
		w.Check(s, "nothing created", check(len(successorSpawns(w)) == 0, "%d successors", len(successorSpawns(w))))
		// Marked as asked and not listed: it cannot be proven never made,
		// so the loop holds for a person and creates nothing else.
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "held for the unsettled successor", all(action(s, "await_user"), reasonHas(s, "successor thread titled"), holds(s, "awaitingUser"),
			check(countsOf(s).ChildSuccessors != nil && countsOf(s).ChildSuccessors.Pending, "status does not say a successor is pending")))
		w.Advance(time.Hour)
		s = w.Tick()
		w.Check(s, "held", all(nothingSent(s), noCalls(s)))
		// The person looks, finds nothing, discards it and resumes.
		r := w.Run("transfer", "replace-children", "-c", w.Config, "--discard-pending-successor")
		s = w.record("discard", r, nil)
		w.Check(s, "discard", exit(s, 0))
		s = w.Control("resume")
		w.Check(s, "resume", exit(s, 0))
		w.Advance(time.Minute)
		s = w.Tick()
		// The discarded attempt stays counted.
		w.Check(s, "both replaced after the discard", all(replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 3), noSendsTo(w, children...)))
	})
	t.Run("seven blocked lanes and the cap of five per handoff", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		armIncident(w, armed{blocked: 7})
		handedOff(w)
		s := w.Tick()
		var want []string
		for i := 1; i <= 5; i++ {
			want = append(want, blockedLane(i))
		}
		c := countsOf(s).ChildSuccessors
		w.Check(s, "five, two skipped at the cap", all(replacedExactly(w, want...), successorsAre(s, 5, 5), check(c != nil && c.SkippedPerRun == 2, "childSuccessors %+v", c)))
		for i := 0; i < 2; i++ {
			w.Advance(time.Hour)
			s = w.Tick()
			w.Check(s, "never a sixth", check(len(successorSpawns(w)) == 5, "%d successors", len(successorSpawns(w))))
		}
	})
	t.Run("the lifetime cap", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		armIncident(w, armed{blocked: 2, lifetime: 1})
		handedOff(w)
		s := w.Tick()
		c := countsOf(s).ChildSuccessors
		w.Check(s, "one, one skipped at the lifetime cap", all(replacedExactly(w, blockedLane(1)), successorsAre(s, 1, 1), check(c != nil && c.SkippedLifetime == 1, "childSuccessors %+v", c)))
		w.Advance(time.Hour)
		s = w.Tick()
		w.Check(s, "never a second", check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))))
	})
	t.Run("the send allowance reached mid-way", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		// The wake, the created thread and one successor.
		armIncident(w, armed{blocked: 2, maxSends: 3})
		handedOff(w)
		s := w.Tick()
		w.Check(s, "one successor, then the allowance", all(replacedExactly(w, blockedLane(1)), sendsUsed(s, 3),
			check(fmt.Sprint(s.Tick["successors"]) != "<nil>" && strings.Contains(fmt.Sprint(s.Tick["successors"]), "stoppedBySendLimit:1"), "successors %v", s.Tick["successors"])))
		for i := 0; i < 2; i++ {
			w.Advance(time.Hour)
			s = w.Tick()
			w.Check(s, "nothing beyond the allowance", all(check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))), sendsUsed(s, 3)))
		}
	})
	for _, hold := range []string{"pause", "stop", "await-user"} {
		t.Run("a "+hold+" between two creations", func(t *testing.T) {
			t.Parallel()
			w := NewWorld(t, scenarioStart)
			children, _ := armIncident(w, armed{blocked: 2})
			handedOff(w)
			// The first successor's spawn is held while the person writes
			// the hold; the check must not start the second.
			s, h := w.tickWithHold(1, hold)
			w.Check(h, "hold saved", exit(h, 0))
			w.Check(s, "the pass ends at the hold", check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))))
			for i := 0; i < 2; i++ {
				w.Advance(20 * time.Minute)
				s = w.Tick()
				w.Check(s, "held", all(nothingSent(s), check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))), ownerIs(w, successor, 2)))
				if hold == "stop" && i == 0 {
					w.Check(s, "the stop reaches the created thread", ranBB(s, "thread", "stop", successor, "--json"))
				}
			}
			s = w.Control("resume")
			w.Check(s, "resume", exit(s, 0))
			w.Advance(time.Minute)
			s = w.Tick()
			w.Check(s, "the pass completes after resume", all(replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 2), noSendsTo(w, children...)))
		})
	}
}

// TestScenarioArmedIncidentOldLimitResets (J): the old provider's limit
// resets while the successor lanes work. Lane recovery is on for the
// takeover, so it may retry lanes in error, but never a replaced one;
// nothing is sent to the old lanes. Switch-back then returns the loop,
// ownership and every child of the created thread (successor lanes too) to
// the coordinator, and no successor is ever created for a successor.
func TestScenarioArmedIncidentOldLimitResets(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	children, reset := armIncident(w, armed{blocked: 2, switchBack: true})
	w.editFallback(initCandidate, func(bb map[string]any) { bb["children"] = map[string]any{"retry_errored": true} })
	handedOff(w)
	s := w.Tick()
	w.Check(s, "successor lanes", replacedExactly(w, blockedLane(1), blockedLane(2)))
	succ := map[string]bool{}
	for _, m := range successorSpawns(w) {
		succ[m.Thread] = true
	}
	for w.Now.Add(12 * time.Hour).Before(reset) {
		w.Advance(12 * time.Hour)
		w.Commit("feat: fictional successor step")
		s = w.Tick()
		w.Check(s, "working", all(noSendsTo(w, children...), check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w)))))
	}
	// The reset: BB retries one old lane by itself; the other stays in
	// error, runnable again. Lane recovery leaves both, as replaced.
	w.At(reset.Add(10 * time.Minute))
	w.Thread(blockedLane(1)).StartTurn()
	s = w.Tick()
	w.Check(s, "the old lanes are left alone", all(noSendsTo(w, children...), check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w)))))
	w.Advance(30 * time.Minute)
	s = w.Tick()
	w.Check(s, "still left alone", noSendsTo(w, children...))
	// Everything finishes; the created thread ends its turn: switch-back.
	for id := range succ {
		w.Thread(id).FinishTurn()
	}
	for _, id := range []string{blockedLane(1), askingLane, codexLane} {
		w.Thread(id).FinishTurn()
	}
	w.Thread(primaryThread).FinishTurn()
	w.Thread(successor).FinishTurn()
	w.Advance(5 * time.Minute)
	s = w.Tick()
	back := append(append([]string{}, children...), successorsOf(w)...)
	w.Check(s, "switch-back with every child", all(
		action(s, "switch_back"), ownerIs(w, primaryThread, 3), childrenOf(w, primaryThread, back...), childrenOf(w, successor),
		check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w))), noSendsTo(w, children...),
	))
	for i := 0; i < 2; i++ {
		w.Advance(time.Hour)
		s = w.Tick()
		w.Check(s, "no successor for a successor", all(check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w))), noSendsTo(w, children...), onlyCreated(w, successorsOf(w)...)))
	}
}

// successorsOf lists the successor lanes created.
func successorsOf(w *World) []string {
	var out []string
	for _, m := range successorSpawns(w) {
		out = append(out, m.Thread)
	}
	return out
}

// onlyCreated: each of these threads received its creation and nothing else.
func onlyCreated(w *World, ids ...string) error {
	for _, id := range ids {
		if err := journeySends(w, id, "spawn"); err != nil {
			return err
		}
	}
	return nil
}

const transferDest = "thr_example_dest"

// transferIncident is a single-route loop owned by its coordinator, with
// two lanes blocked on its provider's limit, one idle and one on codex, and
// a person-made codex conversation to move it to.
func transferIncident(w *World) []string {
	w.t.Helper()
	w.Init(incidentEvents...)
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", exit(s, 0))
	w.Advance(time.Minute)
	w.Tick()
	primary := w.Thread(primaryThread)
	primary.Provider = "claude-code"
	primary.Set(primary.Status())
	reset := w.Now.Add(incidentReset)
	var children []string
	for i := 1; i <= 2; i++ {
		c := primary.Child(blockedLane(i))
		c.Provider, c.Permission = "claude-code", "auto"
		c.StartTurn()
		c.RateLimited(reset)
		children = append(children, c.ID)
	}
	idle := primary.Child(idleLane)
	idle.Provider = "claude-code"
	idle.FinishTurn()
	cx := primary.Child(codexLane)
	cx.Provider = "codex"
	cx.StartTurn()
	dest := w.Thread(transferDest)
	dest.Provider = "codex"
	dest.Set("idle")
	w.Advance(5 * time.Minute)
	primary.RateLimited(reset)
	return append(children, idleLane, codexLane)
}

var providerChange = []string{"--allow-provider-change", "--provider", "codex", "--quota-group", "example-codex", "--model", codexModel, "--reasoning", "medium"}

// TestScenarioTransferWithMixedChildren (K): a person moves the incident's
// loop to a codex conversation with transfer apply. Every child moves,
// whatever its provider; the loop is held for a person; replace-children
// previews, then creates successors for exactly the blocked lanes, uncounted
// against max_sends; a person's resume with no supervisor started is
// flagged once its grace has passed, and the first check clears it.
func TestScenarioTransferWithMixedChildren(t *testing.T) {
	t.Parallel()
	// replace-children is a person's command and judges a lane's reset by
	// the wall clock, as do the control times the alarm measures from: this
	// journey runs on it.
	w := NewWorld(t, time.Now().UTC().Truncate(time.Second))
	children := transferIncident(w)
	r := w.Run(append([]string{"transfer", "apply", "-c", w.Config, "--from", primaryThread, "--thread", transferDest}, providerChange...)...)
	s := w.record("transfer apply", r, nil)
	w.Check(s, "applied", all(exit(s, 0), childrenOf(w, transferDest, children...), childrenOf(w, primaryThread), holds(s, "awaitingUser"),
		check(s.Status.TargetAgent.BBThread == transferDest, "target thread %s", s.Status.TargetAgent.BBThread), noSendsTo(w, children...), nothingSent(s)))
	r = w.Run("transfer", "replace-children", "-c", w.Config, "--dry-run", "--json")
	s = w.record("replace-children --dry-run", r, nil)
	w.Check(s, "preview", all(exit(s, 0), nothingSent(s), check(strings.Contains(r.Stdout, `"wouldCreate":2`), "preview %s", r.Stdout)))
	r = w.Run("transfer", "replace-children", "-c", w.Config, "--model", childModel, "--reasoning", childReasoning, "--json")
	s = w.record("replace-children", r, nil)
	var made []string
	for _, m := range s.Sent {
		if m.Kind == "spawn" && m.Parent == transferDest && m.Provider == "codex" && m.Model == childModel {
			made = append(made, m.Thread)
		}
	}
	w.Check(s, "two successors under the destination", all(exit(s, 0), check(len(made) == 2 && len(s.Sent) == 2, "sent %s", describeSends(s.Sent)), noSendsTo(w, children...)))
	r = w.Run("transfer", "replace-children", "-c", w.Config, "--model", childModel, "--json")
	s = w.record("replace-children again", r, nil)
	w.Check(s, "never twice", all(exit(s, 0), nothingSent(s)))
	// A person resumes and starts no supervisor.
	s = w.Control("resume")
	w.Check(s, "resume", exit(s, 0))
	alarm := func(at time.Time) bool {
		st := w.Run("status", "-c", w.Config, "--json", "--now", at.Format(time.RFC3339))
		var v struct {
			Schedule struct {
				Overdue bool `json:"overdue"`
			} `json:"schedule"`
		}
		json.Unmarshal([]byte(st.Stdout), &v)
		return v.Schedule.Overdue
	}
	w.Check(s, "not alarmed at once", check(!alarm(w.Now), "alarmed at once"))
	w.Check(s, "alarmed later", check(alarm(w.Now.Add(3*time.Hour)), "not alarmed after three hours"))
	w.Advance(time.Hour)
	s = w.Tick()
	w.Check(s, "a check clears it", all(exit(s, 0), check(!alarm(w.Now.Add(time.Minute)), "still alarmed after a check")))
}

// TestScenarioTransferInterruptedThenRecovered (K): transfer apply is killed
// in the middle of moving the children; transfer recover with its ID moves
// the rest, each once, and a second recover changes nothing.
func TestScenarioTransferInterruptedThenRecovered(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	children := transferIncident(w)
	if err := os.WriteFile(filepath.Join(w.world, "kill.update"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.Run(append([]string{"transfer", "apply", "-c", w.Config, "--from", primaryThread, "--thread", transferDest}, providerChange...)...)
	s := w.record("transfer apply (killed)", r, nil)
	w.Check(s, "killed after two moves", all(check(r.Code == -1, "exit %d", r.Code), check(len(s.Moves) == 2, "%d moves", len(s.Moves))))
	st := stateOf(w)
	if st.ChildMove == nil || st.ChildMove.Transfer == "" {
		t.Fatalf("the interrupted move is not on record: %+v", st.ChildMove)
	}
	id := st.ChildMove.Transfer
	r = w.Run("transfer", "recover", "-c", w.Config, "--id", id)
	s = w.record("transfer recover", r, nil)
	w.Check(s, "recovered", all(exit(s, 0), childrenOf(w, transferDest, children...), childrenOf(w, primaryThread), holds(s, "awaitingUser"), nothingSent(s)))
	r = w.Run("transfer", "recover", "-c", w.Config, "--id", id)
	s = w.record("transfer recover again", r, nil)
	w.Check(s, "a no-op once done", all(exit(s, 0), check(len(s.Moves) == 0, "moved %v", s.Moves)))
	n := map[string]int{}
	for _, st := range w.Journal {
		for _, m := range st.Moves {
			n[m.Child]++
			w.Check(st, "moved only to the destination", check(m.From == primaryThread && m.To == transferDest, "moved %s from %s to %s", m.Child, m.From, m.To))
		}
	}
	for _, c := range children {
		w.Check(s, "each child moved once", check(n[c] == 1, "%s moved %d times", c, n[c]))
	}
}
