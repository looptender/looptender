package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// The takeover scenarios replay the incident that five separately reviewed
// changes were made for, with all of them in one build: a coordinator loop on
// a BB thread of one provider, owned by that thread, with five direct child
// threads (two in error on the same provider), reaches a usage limit that
// resets in 132 hours. Its only fallback starts a new BB thread on another
// provider. Ownership follows the handoff to the created thread, the
// coordinator's children follow it there, the created thread's first prompt
// carries the counts, and the standing wake policy is the build's or the
// file's. Each scenario then breaks it: a crash at each external step, a
// hold at each point, no fallback at all, a switch-back at the reset, and a
// prompt file that follows or does not follow policy updates.

const (
	codexCandidate = "codex-new-conversation"
	codexModel     = "example-codex-model"
	successor      = "thr_example_new1"
	incidentReset  = 132 * time.Hour
)

// codexNewThreadFallback starts a new BB thread on another provider.
const codexNewThreadFallback = `  - id: codex-new-conversation
    adapter: bb
    provider: codex
    group: codex-account-y
    bb: {new_thread: true, cli: "%[1]s/bb", model: example-codex-model}
    probe: {model: example-codex-model, cli: "%[1]s/codex"}
`

// lanes are the coordinator's five direct children: two working, one idle,
// and two in error on the coordinator's provider.
var lanes = []string{"thr_example_lane1", "thr_example_lane2", "thr_example_lane3", "thr_example_lane4", "thr_example_lane5"}

type incident struct {
	// fallback configures the new-thread fallback; without it the loop has
	// a single route, as in the real incident.
	fallback   bool
	switchBack bool
	// events are the report events; nil leaves the loop without a report
	// section.
	events []string
	// prompt, when set, rewrites the prompt file before the loop is enabled.
	prompt func(w *World)
}

// incidentEvents are the reports a configured incident loop subscribes to.
var incidentEvents = []string{"handoff", "switch_back", "await_user", "limit_wait"}

// setupIncident creates the loop, enables it from the coordinator's own
// thread so that thread owns it, and wakes the coordinator once.
func setupIncident(w *World, o incident) {
	w.t.Helper()
	w.Init(o.events...)
	if o.fallback {
		w.Fallbacks(fallbacksFile(codexNewThreadFallback), func(l map[string]any) {
			l["group"] = "claude-account-a"
			errs, _ := l["errors"].(map[string]any)
			errs["failover_on"] = []string{"rate_limit"}
			if o.switchBack {
				l["switch_back"] = map[string]any{"enabled": true, "min_after": "1h", "retry_every": "30m"}
			}
			l["probe"] = map[string]any{"cli": w.BinPath("claude")}
		})
	}
	if o.prompt != nil {
		o.prompt(w)
	}
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", all(exit(s, 0), ownerIs(w, primaryThread, 1)))
	w.Advance(time.Minute)
	s = w.Tick()
	w.Check(s, "first wake", all(action(s, "wake"), sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"})))
}

// startLanes gives the coordinator its five children while it works; the
// last two reach the provider's limit.
func startLanes(w *World) {
	primary := w.Thread(primaryThread)
	for i, id := range lanes {
		c := primary.Child(id)
		switch {
		case i < 2:
			c.StartTurn()
		case i >= 3:
			c.StartTurn()
			c.RateLimited(w.Now.Add(incidentReset))
		}
	}
}

// hitLimit puts the coordinator at a usage limit resetting in 132 hours.
func hitLimit(w *World) time.Time {
	reset := w.Now.Add(incidentReset)
	w.Thread(primaryThread).RateLimited(reset)
	return reset
}

// handoffChecked is what the check that hands off must have done.
func handoffChecked(w *World, s *Step, reports bool) {
	w.t.Helper()
	want := []string{}
	if reports {
		want = []string{"keepgoing.handoff"}
	}
	w.Check(s, "handoff to a created thread", all(
		action(s, "handoff"), spawns(w, 1), sentExactly(s, Send{Kind: "spawn", Thread: successor, Mode: "accept-edits"}),
		reported(s, want...), holds(s), targetIs(s, codexCandidate, successor), newThreads(s, 1),
	))
	if len(s.Sent) == 1 {
		m := s.Sent[0].Message
		w.Check(s, "the first prompt", all(
			check(strings.Contains(m, "Continue from @thread:"+primaryThread+"."), "does not continue the coordinator"),
			check(strings.Contains(m, `this conversation owns the "example-loop" loop`), "does not say ownership follows"),
			check(strings.Contains(m, "At the last check it saw 5 direct child thread(s), 2 of them in error."), "does not carry the child counts"),
		))
	}
}

// converged is the end state of the incident: one created thread, the
// loop on it, the loop owned by it, the coordinator's children under it,
// and nothing sent to any child or a second time to the coordinator.
func converged(w *World, s *Step) {
	w.t.Helper()
	w.Check(s, "converged", all(
		spawns(w, 1), targetIs(s, codexCandidate, successor), ownerIs(w, successor, 2),
		childrenOf(w, successor, lanes...), childrenOf(w, primaryThread),
		childMoveIs(s, 5, 0), noSendsToLanes(w), journeySends(w, primaryThread, "tell"),
		movedOnce(w, primaryThread, successor),
	))
}

// TestScenarioIncidentTakeover is the incident end to end: one created
// thread, ownership and children moved to it, the coordinator's commands
// refused, and, without switch-back, nothing at all for the coordinator
// when its limit resets.
func TestScenarioIncidentTakeover(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	setupIncident(w, incident{fallback: true, events: incidentEvents})
	w.Advance(5 * time.Minute)
	startLanes(w)
	w.Advance(5 * time.Minute)
	reset := hitLimit(w)
	w.Advance(time.Minute)
	s := w.Tick()
	handoffChecked(w, s, true)
	converged(w, s)
	w.Check(s, "the tick says what moved", check(s.Tick["ownerMovedTo"] == successor && s.Tick["childrenMoved"] == 5.0,
		"ownerMovedTo %v childrenMoved %v", s.Tick["ownerMovedTo"], s.Tick["childrenMoved"]))
	if r := s.Reports; len(r) == 1 {
		w.Check(s, "the handoff report names no thread", check(!strings.Contains(fmt.Sprint(r[0].Body), "thr_example"), "report %v", r[0].Body))
	}

	oldThreadRefused(w)

	// The created thread works for the 132 hours; nothing goes to the
	// coordinator or its former children, and nothing switches back.
	for at := w.Now; w.Now.Before(reset); {
		w.Advance(12 * time.Hour)
		w.Commit(fmt.Sprintf("feat: fictional successor step at %s", w.Now.Sub(at)))
		s = w.Tick()
		w.Check(s, "successor working", all(action(s, "none"), nothingSent(s), targetIs(s, codexCandidate, successor), notProbed(s, cliDefault)))
	}
	// The coordinator's limit has reset; BB itself left it in error.
	w.At(reset.Add(10 * time.Minute))
	w.Thread(successor).FinishTurn()
	s = w.Tick()
	w.Check(s, "after the reset the successor is woken, not switched", all(
		action(s, "wake"), sentExactly(s, Send{Kind: "tell", Thread: successor, Mode: "auto"}), noReports(s),
		targetIs(s, codexCandidate, successor), notProbed(s, cliDefault),
	))
	converged(w, s)
}

// oldThreadRefused checks that the thread ownership left refuses control
// commands and changes nothing, while a person's commands still work.
func oldThreadRefused(w *World) {
	w.t.Helper()
	before := readFile(w, filepath.Join(w.runtimeDir(), control.FileName))
	for _, cmd := range [][]string{{"resume"}, {"enable"}, {"retarget", "primary"}} {
		r := w.RunAs(primaryThread, append([]string{cmd[0], "-c", w.Config}, cmd[1:]...)...)
		// Under a running daemon a command can meet a check in progress
		// (exit 75, the loop's lock is busy); it is asked again.
		for i := 0; r.Code == 75 && i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			r = w.RunAs(primaryThread, append([]string{cmd[0], "-c", w.Config}, cmd[1:]...)...)
		}
		s := w.record("old owner: "+strings.Join(cmd, " "), r, nil)
		w.Check(s, "old owner refused", all(
			exit(s, 1),
			check(strings.Contains(r.Stderr, "ownership of this loop moved from this thread to BB thread "+successor+" with the supervisor's handoff"), "stderr %q", r.Stderr),
		))
	}
	w.Check(w.Journal[len(w.Journal)-1], "a refused command changes nothing", check(readFile(w, filepath.Join(w.runtimeDir(), control.FileName)) == before, "control.json changed"))
}

// ownerIs compares the loop's recorded owner and generation.
func ownerIs(w *World, thread string, generation uint64) error {
	f, err := control.Load(w.runtimeDir())
	if err != nil {
		return err
	}
	if f.Ownership == nil {
		return fmt.Errorf("no owner, want %s", thread)
	}
	return check(f.Ownership.ThreadID == thread && f.Ownership.Generation == generation,
		"owner %s (generation %d), want %s (generation %d)", f.Ownership.ThreadID, f.Ownership.Generation, thread, generation)
}

// childrenOf compares the children bb lists under parent, in any order.
func childrenOf(w *World, parent string, want ...string) error {
	var listed []struct {
		ID string `json:"id"`
	}
	b, err := os.ReadFile(filepath.Join(w.world, parent+".children"))
	if err == nil {
		json.Unmarshal(b, &listed)
	}
	got := []string{}
	for _, c := range listed {
		got = append(got, c.ID)
	}
	sort.Strings(got)
	want = append([]string{}, want...)
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	return check(fmt.Sprint(got) == fmt.Sprint(want), "children of %s: %v, want %v", parent, got, want)
}

// childMoveIs compares the counts status gives for the child move.
func childMoveIs(s *Step, moved, pending int) error {
	var st struct {
		ChildMove *struct {
			Moved, Pending, Failed int
			Done                   bool
		} `json:"childMove"`
	}
	json.Unmarshal([]byte(s.Status.Raw), &st)
	if st.ChildMove == nil {
		return fmt.Errorf("status has no childMove")
	}
	m := st.ChildMove
	return check(m.Moved == moved && m.Pending == pending && m.Failed == 0 && m.Done == (pending == 0),
		"childMove moved %d pending %d failed %d done %v, want %d/%d", m.Moved, m.Pending, m.Failed, m.Done, moved, pending)
}

// noSendsToLanes: no child is ever messaged, retried or created.
func noSendsToLanes(w *World) error {
	for _, id := range lanes {
		if err := journeySends(w, id); err != nil {
			return err
		}
	}
	return nil
}

// movedOnce: over the whole journey each lane was moved from one thread to
// the other exactly once, and never anywhere else.
func movedOnce(w *World, from, to string) error {
	n := map[string]int{}
	for _, st := range w.Journal {
		for _, m := range st.Moves {
			if m.From != from || m.To != to {
				return fmt.Errorf("moved %s from %s to %s", m.Child, m.From, m.To)
			}
			n[m.Child]++
		}
	}
	for _, id := range lanes {
		if n[id] != 1 {
			return fmt.Errorf("%s moved %d times", id, n[id])
		}
	}
	return nil
}

func readFile(w *World, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// stateOf reads state.json.
func stateOf(w *World) state.File {
	w.t.Helper()
	st, err := state.Load(w.runtimeDir())
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

// crashPoint is one place a supervisor can stop in the middle of the
// incident's handoff check. Each leaves what a SIGKILL at that moment
// leaves; the check process is the supervisor (a daemon runs the same check
// in its own process, so killing a daemon there leaves the same files).
type crashPoint struct {
	name string
	// crash runs the handoff check so that it stops at the point. It
	// returns the step it recorded.
	crash func(w *World) *Step
	// owner is the owner the crash leaves on record.
	owner string
	// recorded: the handoff to the created thread is in state.json.
	recorded bool
}

var crashPoints = []crashPoint{
	{"after the thread is created and before it is recorded", func(w *World) *Step {
		w.killAfter("spawn")
		s := w.killedTick("killed after spawn")
		w.Check(s, "created once, counted, not recorded", all(spawns(w, 1), targetIs(s, "primary", primaryThread), newThreads(s, 1)))
		return s
	}, primaryThread, false},
	{"while the handoff is reported", func(w *World) *Step {
		return killDuringReport(w, "keepgoing.handoff", "killed while reporting the handoff")
	}, primaryThread, false},
	{"after state.json is saved and before control.json", crashBeforeOwnerSave, primaryThread, true},
	{"in the middle of the child moves", func(w *World) *Step {
		// The second parent change kills the check: one child has moved.
		if err := os.WriteFile(filepath.Join(w.world, "kill.update"), []byte("2"), 0o600); err != nil {
			w.t.Fatal(err)
		}
		s := w.killedTick("killed mid child move")
		w.Check(s, "two of five children asked to move", check(len(s.Moves) == 2, "%d moves", len(s.Moves)))
		return s
	}, primaryThread, false},
}

// killDuringReport runs one check and kills it while the report of type typ
// arrives at the receiver.
func killDuringReport(w *World, typ, label string) *Step {
	w.t.Helper()
	cmd := exec.Command(keepgoingBin, "tick", "-c", w.Config, "--now", w.stamp())
	cmd.Dir = w.Root
	cmd.Env = append(append([]string{}, w.env...), "BB_THREAD_ID=")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// The receiver runs on its own goroutine; it kills by pid through a
	// Process of its own.
	var pid atomic.Int64
	w.Hooks.OnReport(func(got string) {
		if got != typ || pid.Load() <= 0 {
			return
		}
		if p, err := os.FindProcess(int(pid.Load())); err == nil {
			p.Kill()
		}
	})
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	pid.Store(int64(cmd.Process.Pid))
	cmd.Wait()
	w.Hooks.OnReport(nil)
	s := w.record(label, Result{Code: cmd.ProcessState.ExitCode(), Stdout: out.String(), Stderr: errb.String()}, nil)
	if s.Result.Code != -1 {
		w.Failf(s, "the check was not killed: exit %d", s.Result.Code)
	}
	return s
}

// crashBeforeOwnerSave leaves what a supervisor killed between saving the
// handoff check's state.json and changing control.json leaves: the check's
// state with the ownership move pending, and the owner unchanged. No
// external command runs between the two writes, so the kill cannot be
// placed from outside; the check runs to its end and the files are put back
// to that moment: control.json as it was, and the move pending in state.json.
func crashBeforeOwnerSave(w *World) *Step {
	w.t.Helper()
	ctl := filepath.Join(w.runtimeDir(), control.FileName)
	before := readFile(w, ctl)
	s := w.Tick()
	handoffChecked(w, s, true)
	if err := os.WriteFile(ctl, []byte(before), 0o600); err != nil {
		w.t.Fatal(err)
	}
	st := stateOf(w)
	if st.PendingOwnerMove != nil {
		w.t.Fatalf("the check left a pending owner move: %+v", st.PendingOwnerMove)
	}
	st.PendingOwnerMove = &state.OwnerMove{From: primaryThread, FromGeneration: 1, To: successor, Reason: "new_thread", At: w.Now}
	if err := state.Save(w.runtimeDir(), st); err != nil {
		w.t.Fatal(err)
	}
	return s
}

// TestScenarioIncidentCrash stops the supervisor at each external step of
// the incident's handoff and checks that the next check converges on the
// incident's end state: no second thread, no owner lost, no child moved
// twice or anywhere else.
func TestScenarioIncidentCrash(t *testing.T) {
	t.Parallel()
	for _, p := range crashPoints {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			w := NewWorld(t, scenarioStart)
			setupIncident(w, incident{fallback: true, events: incidentEvents})
			w.Advance(5 * time.Minute)
			startLanes(w)
			w.Advance(5 * time.Minute)
			hitLimit(w)
			w.Advance(time.Minute)
			s := p.crash(w)
			w.Check(s, "the owner the crash leaves", ownerIs(w, p.owner, 1))
			w.Advance(time.Minute)
			s = w.Tick()
			w.Check(s, "restart", nothingSent(s))
			converged(w, s)
			for i := 0; i < 3; i++ {
				w.Advance(70 * time.Minute)
				w.Commit(fmt.Sprintf("feat: fictional successor step %d", i))
				s = w.Tick()
				w.Check(s, "stays converged", all(nothingSent(s), check(len(s.Moves) == 0, "moved %v", s.Moves)))
				converged(w, s)
			}
			oldThreadRefused(w)
		})
	}
}

// TestScenarioIncidentHolds writes a stop, a pause or an await-user hold at
// each point of the incident: before the handoff, after each crash point and
// after the handoff. Held checks create, move and send nothing and leave the
// owner where it is; a person's resume then converges on the end state.
func TestScenarioIncidentHolds(t *testing.T) {
	t.Parallel()
	points := append([]crashPoint{
		{"before the handoff", func(w *World) *Step { return nil }, primaryThread, false},
	}, crashPoints...)
	points = append(points, crashPoint{"after the handoff", func(w *World) *Step {
		s := w.Tick()
		handoffChecked(w, s, true)
		return s
	}, successor, true})
	for _, p := range points {
		for _, hold := range []string{"pause", "stop", "await-user"} {
			t.Run(p.name+"/"+hold, func(t *testing.T) {
				t.Parallel()
				w := NewWorld(t, scenarioStart)
				setupIncident(w, incident{fallback: true, events: incidentEvents})
				w.Advance(5 * time.Minute)
				startLanes(w)
				w.Advance(5 * time.Minute)
				hitLimit(w)
				w.Advance(time.Minute)
				p.crash(w)
				generation := uint64(1)
				if p.owner == successor {
					generation = 2
				}
				spawned := 0
				if p.name != "before the handoff" {
					spawned = 1
				}
				// A person holds the loop.
				w.Advance(time.Minute)
				s := w.Control(hold, "--note", "Fictional hold by a person")
				w.Check(s, "hold saved", exit(s, 0))
				stopped := map[string]bool{}
				for i := 0; i < 3; i++ {
					w.Advance(20 * time.Minute)
					s = w.Tick()
					for _, c := range s.Calls {
						if len(c) > 2 && c[0] == "thread" && c[1] == "stop" {
							stopped[c[2]] = true
						}
					}
					w.Check(s, "held", all(
						nothingSent(s), spawns(w, spawned), check(len(s.Moves) == 0, "moved %v", s.Moves),
						ownerIs(w, p.owner, generation), noReports(s), noSendsToLanes(w),
					))
				}
				if hold == "stop" && p.recorded {
					// The created thread is running the task: the stop
					// reaches it. Before the handoff is recorded the stop
					// goes to the coordinator only; that is open work and is
					// not asserted here.
					w.Check(s, "the stop reaches the thread doing the work", check(stopped[successor], "stopped %v", stopped))
				}
				s = w.Control("resume")
				w.Check(s, "a person resumes", exit(s, 0))
				w.Advance(time.Minute)
				s = w.Tick()
				for i := 0; i < 2; i++ {
					w.Advance(20 * time.Minute)
					s = w.Tick()
				}
				converged(w, s)
			})
		}
	}
}

// TestScenarioIncidentNoFallback is the incident as it happened: no
// fallback, and no report section. The wait for the 132-hour limit is
// recorded and shown in status, doctor and the panel with the "nobody was
// told" wording; nothing is created, moved, sent or reported.
func TestScenarioIncidentNoFallback(t *testing.T) {
	t.Parallel()
	// doctor and the panel read the wall clock, so this journey ends a few
	// minutes before it.
	w := NewWorld(t, time.Now().UTC().Truncate(time.Minute).Add(-4*time.Hour-30*time.Minute))
	setupIncident(w, incident{})
	w.Advance(5 * time.Minute)
	startLanes(w)
	w.Advance(5 * time.Minute)
	reset := hitLimit(w)
	for i := 0; i < 4; i++ {
		w.Advance(time.Hour)
		s := w.Tick()
		w.Check(s, "the long wait", all(
			action(s, "none"), nothingSent(s), noReports(s), spawns(w, 0), check(len(s.Moves) == 0, "moved %v", s.Moves),
			targetIs(s, "primary", primaryThread), ownerIs(w, primaryThread, 1), holds(s), noSendsToLanes(w),
		))
		var st struct {
			LimitWait struct {
				State, Reason, ResetIn string
				ReportConfigured       *bool
				ResetsAt               *time.Time
			} `json:"limitWait"`
		}
		json.Unmarshal([]byte(s.Status.Raw), &st)
		lw := st.LimitWait
		w.Check(s, "status --json records the wait", all(
			check(lw.State == "not_subscribed", "state %q", lw.State),
			// init leaves rate_limit out of errors.failover_on for a loop
			// with no fallback, so failover is off for the limit.
			check(lw.Reason == "limit_failover_disabled", "reason %q", lw.Reason),
			check(lw.ReportConfigured != nil && !*lw.ReportConfigured, "reportConfigured %v", lw.ReportConfigured),
			check(lw.ResetsAt != nil && lw.ResetsAt.Equal(reset), "resetsAt %v, want %v", lw.ResetsAt, reset),
			check(lw.ResetIn == fmtResetIn(reset.Sub(w.Now)), "resetIn %q, want %q", lw.ResetIn, fmtResetIn(reset.Sub(w.Now))),
		))
	}
	text := w.Run("status", "-c", w.Config, "--now", w.stamp())
	s := w.Journal[len(w.Journal)-1]
	w.Check(s, "status says nobody was told", all(
		check(strings.Contains(text.Stdout, "LONG LIMIT WAIT"), "status:\n%s", text.Stdout),
		check(strings.Contains(text.Stdout, "NOBODY WAS TOLD"), "status:\n%s", text.Stdout),
	))
	doc := w.Run("doctor", "-c", w.Config, "--json")
	w.Check(s, "doctor warns of the wait and the single route", all(
		check(strings.Contains(doc.Stdout, `"limit_wait"`), "doctor:\n%s%s", doc.Stdout, doc.Stderr),
		check(strings.Contains(doc.Stdout, `"routes.single"`), "doctor:\n%s", doc.Stdout),
	))
	panel := w.panelJSON()
	w.Check(s, "the panel's card says nobody was told", all(
		check(strings.Contains(panel, `"waiting_on_limit"`), "panel:\n%s", panel),
		check(strings.Contains(panel, `"told":"no_report_section"`), "panel:\n%s", panel),
		check(strings.Contains(panel, "Nobody was told: the loop had no report section when the wait was recorded."), "panel:\n%s", panel),
	))
}

// fmtResetIn is how status words the time left: days and hours.
func fmtResetIn(d time.Duration) string {
	h := int(d.Hours())
	if h >= 24 {
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	return fmt.Sprintf("%dh", h)
}

// panelJSON serves the read-only panel for this world's loops, reads its
// JSON once and stops it.
func (w *World) panelJSON() string {
	w.t.Helper()
	cmd := exec.Command(keepgoingBin, "dock", "--offline", "--port", "0")
	cmd.Dir = w.Root
	cmd.Env = w.env
	out, err := cmd.StdoutPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	var url string
	buf := make([]byte, 4096)
	var seen string
	for deadline := time.Now().Add(10 * time.Second); url == "" && time.Now().Before(deadline); {
		n, err := out.Read(buf)
		seen += string(buf[:n])
		for _, f := range strings.Fields(seen) {
			if strings.HasPrefix(f, "http://127.0.0.1:") {
				url = f
			}
		}
		if err != nil {
			break
		}
	}
	if url == "" {
		w.t.Fatalf("dock printed no address: %q", seen)
	}
	resp, err := http.Get(strings.TrimSuffix(url, "/") + "/api/v1/dock")
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestScenarioIncidentSwitchBack is the incident with switch-back on: at
// the reset the loop returns to the coordinator, ownership returns with it
// as another step of the same run, every child of the created thread
// (including one it started) moves back, and the created thread's commands
// are refused from then on.
func TestScenarioIncidentSwitchBack(t *testing.T) {
	t.Parallel()
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "uninterrupted", true: "killed in the middle of the moves back"}[crash], func(t *testing.T) {
			t.Parallel()
			switchBackJourney(t, crash)
		})
	}
}

func switchBackJourney(t *testing.T, crash bool) {
	w := NewWorld(t, scenarioStart)
	setupIncident(w, incident{fallback: true, switchBack: true, events: incidentEvents})
	w.Advance(5 * time.Minute)
	startLanes(w)
	w.Advance(5 * time.Minute)
	reset := hitLimit(w)
	w.Advance(time.Minute)
	s := w.Tick()
	handoffChecked(w, s, true)
	converged(w, s)

	// The successor starts a lane of its own and works until the reset.
	w.Advance(time.Hour)
	w.Thread(successor).Child("thr_example_lane6").StartTurn()
	for w.Now.Add(12 * time.Hour).Before(reset) {
		w.Advance(12 * time.Hour)
		w.Commit("feat: fictional successor step")
		s = w.Tick()
		w.Check(s, "no switch-back before the reset", all(nothingSent(s), targetIs(s, codexCandidate, successor), check(len(s.Moves) == 0, "moved %v", s.Moves)))
	}
	// Active children hold back a switch-back (documented); the working
	// lanes finish before the reset.
	for _, id := range []string{"thr_example_lane1", "thr_example_lane2", "thr_example_lane6"} {
		w.Thread(id).FinishTurn()
	}
	w.At(reset.Add(10 * time.Minute))
	w.Thread(primaryThread).FinishTurn() // BB's own retry after the reset.
	w.Thread(successor).FinishTurn()
	var moves []Move
	if crash {
		// The third parent change kills the check: the switch-back moves
		// children before it briefs the coordinator or saves anything.
		if err := os.WriteFile(filepath.Join(w.world, "kill.update"), []byte("3"), 0o600); err != nil {
			t.Fatal(err)
		}
		s = w.killedTick("killed moving children back")
		w.Check(s, "killed after the brief, before anything was saved", all(
			sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"}), noReports(s), ownerIs(w, successor, 2)))
		moves = append(moves, s.Moves...)
		// As documented for a kill right after a switch-back brief: the
		// next check holds for a person, who finds the coordinator took the
		// task, retargets the loop to it and resumes.
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "held for a person", all(action(s, "await_user"), nothingSent(s), holds(s, "awaitingUser"), reported(s, "keepgoing.await_user")))
		moves = append(moves, s.Moves...)
		w.Advance(10 * time.Minute)
		s = w.Control("retarget", "primary")
		w.Check(s, "retarget", exit(s, 0))
		s = w.Control("resume")
		w.Check(s, "resume", exit(s, 0))
		w.Advance(time.Minute)
		s = w.Tick()
		moves = append(moves, s.Moves...)
		w.Check(s, "back on the coordinator without a second brief", all(nothingSent(s), targetIs(s, "primary", primaryThread)))
	} else {
		s = w.Tick()
		moves = append(moves, s.Moves...)
		w.Check(s, "switch-back", all(
			action(s, "switch_back"), sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"}), reported(s, "keepgoing.switch_back"),
			check(s.Tick["ownerMovedTo"] == primaryThread, "ownerMovedTo %v", s.Tick["ownerMovedTo"]),
		))
	}
	back := append([]string{"thr_example_lane6"}, lanes...)
	w.Check(s, "back on the coordinator", all(
		targetIs(s, "primary", primaryThread), spawns(w, 1), journeySends(w, primaryThread, "tell", "tell"),
		childrenOf(w, primaryThread, back...), childrenOf(w, successor),
		check(len(moves) == len(back), "%d moves, want %d", len(moves), len(back)), noSendsToLanes(w),
	))
	for _, m := range moves {
		w.Check(s, "moved back to the coordinator", check(m.From == successor && m.To == primaryThread, "moved %s from %s to %s", m.Child, m.From, m.To))
	}

	// The created thread's commands are refused now; the coordinator's work.
	// After the kill, the person's retarget settles the brief and the child
	// moves complete, but the ownership move the switch-back had not saved
	// is not made; where ownership should end up then is open work, so it is
	// not asserted on that path.
	if !crash {
		w.Check(s, "ownership returned", ownerIs(w, primaryThread, 3))
		before := readFile(w, filepath.Join(w.runtimeDir(), control.FileName))
		for _, cmd := range []string{"resume", "enable"} {
			r := w.RunAs(successor, cmd, "-c", w.Config)
			s = w.record("successor: "+cmd, r, nil)
			w.Check(s, "successor refused", all(exit(s, 1),
				check(strings.Contains(r.Stderr, "ownership of this loop moved from this thread to BB thread "+primaryThread+" with the supervisor's handoff"), "stderr %q", r.Stderr)))
		}
		w.Check(s, "a refused command changes nothing", check(readFile(w, filepath.Join(w.runtimeDir(), control.FileName)) == before, "control.json changed"))
		s = w.record("coordinator: enable", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
		w.Check(s, "the coordinator owns the loop again", all(exit(s, 0), ownerIs(w, primaryThread, 3)))
	}

	// Nothing more for the created thread.
	for i := 0; i < 3; i++ {
		w.Advance(time.Hour)
		w.Commit("feat: fictional coordinator step")
		s = w.Tick()
		w.Check(s, "back on the coordinator", all(sentTo(s, successor), targetIs(s, "primary", primaryThread), check(len(s.Moves) == 0, "moved %v", s.Moves), spawns(w, 1)))
	}
	w.Check(s, "the successor got only its first prompt", journeySends(w, successor, "spawn"))
}

// policyV1 is the first standing wake policy setup wrote, exactly as
// internal/prompt keeps it (version 1).
const policyV1 = `Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

Only when the ENTIRE task is completed and verified, run:
%[1]s complete -c %[2]s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%[1]s await-user -c %[2]s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`

// currentPolicyOnly is a sentence of the current wording that version 1
// does not have.
const currentPolicyOnly = "For a coordinator, reconcile existing direct children"

// TestScenarioIncidentWakePolicy: a loop whose prompt file is the one
// setup recorded sends the build's current wording, after the handoff too,
// in the created thread's first prompt and in later wakes; a loop whose file
// was changed after it was recorded sends that file exactly as written.
func TestScenarioIncidentWakePolicy(t *testing.T) {
	t.Parallel()
	for _, follows := range []bool{true, false} {
		t.Run(map[bool]string{true: "following", false: "not following"}[follows], func(t *testing.T) {
			t.Parallel()
			w := NewWorld(t, scenarioStart)
			var file string
			writePrompt := func(w *World) {
				path := filepath.Join(filepath.Dir(w.Config), "prompt.md")
				file = fmt.Sprintf(policyV1, "looptender", w.Config) + scenarioTask + "\n"
				recorded := file
				if !follows {
					recorded = file + "\n"
				}
				sum := sha256.Sum256([]byte(recorded))
				for name, b := range map[string]string{
					path:                       file,
					path + ".wake-policy.json": fmt.Sprintf(`{"version":1,"policyVersion":1,"sha256":%q}`, hex.EncodeToString(sum[:])),
				} {
					if err := os.WriteFile(name, []byte(b), 0o600); err != nil {
						w.t.Fatal(err)
					}
				}
			}
			setupIncident(w, incident{fallback: true, events: incidentEvents, prompt: writePrompt})
			wording := func(s *Step, what string) {
				w.t.Helper()
				for _, m := range s.Sent {
					if follows {
						w.Check(s, what+" carries the current wording", all(
							check(strings.Contains(m.Message, currentPolicyOnly), "no current wording"),
							check(strings.Contains(m.Message, scenarioTask), "no task"),
						))
					} else {
						w.Check(s, what+" carries the file as written", all(
							check(strings.Contains(m.Message, strings.TrimSpace(file)), "not the file as written"),
							check(!strings.Contains(m.Message, currentPolicyOnly), "current wording sent"),
						))
					}
				}
			}
			wake := w.Journal[len(w.Journal)-1]
			wording(wake, "the first wake")
			var st struct {
				WakePolicy struct{ Source string } `json:"wakePolicy"`
			}
			json.Unmarshal([]byte(wake.Status.Raw), &st)
			want := map[bool]string{true: "following", false: "generated_not_following"}[follows]
			w.Check(wake, "status names the policy source", check(st.WakePolicy.Source == want, "wakePolicy.source %q, want %q", st.WakePolicy.Source, want))

			w.Advance(10 * time.Minute)
			hitLimit(w)
			w.Advance(time.Minute)
			s := w.Tick()
			w.Check(s, "handoff", all(action(s, "handoff"), spawns(w, 1), ownerIs(w, successor, 2)))
			wording(s, "the created thread's first prompt")
			w.Advance(time.Hour)
			w.Thread(successor).FinishTurn()
			s = w.Tick()
			w.Check(s, "the successor is woken", all(action(s, "wake"), sentExactly(s, Send{Kind: "tell", Thread: successor, Mode: "auto"})))
			wording(s, "a later wake")
		})
	}
}
