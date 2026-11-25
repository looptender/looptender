package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// An observed sessionpipe target is marked observed in status, the dock and
// the panel list, and its can never offers stop: pause and resume change only
// whether looptender checks the session.
func TestDockSaysAnObservedTargetIsNeverWoken(t *testing.T) {
	observed := func(s *statusOut) {
		s.TargetAgent = refOf(config.Agent{Adapter: config.SessionpipeAdapter, Provider: config.SessionpipeAdapter,
			Sessionpipe: &config.Sessionpipe{Session: "claude-code:example", EventsFile: "/example/events.jsonl"}})
	}
	for _, tc := range []struct {
		name     string
		controls *clientControls
		want     []string
	}{
		{"running", &clientControls{Enabled: true}, []string{"pause"}},
		{"paused", &clientControls{Enabled: true, Paused: true}, []string{"resume"}},
		{"held", &clientControls{Enabled: true, AwaitingUser: true}, []string{"pause", "resume"}},
	} {
		l, _ := projectDockLoop(dockEvidence{controls: tc.controls, status: observed}.input(), dockNow, defaultStaleAfter, false)
		if !reflect.DeepEqual(l.Can, tc.want) {
			t.Errorf("%s: can %v, want %v", tc.name, l.Can, tc.want)
		}
		if !l.Observed || !strings.Contains(l.ObservedNote, "never sends to, wakes, re-prompts or stops") || l.CurrentRoute.Tool != "Sessionpipe (observed)" {
			t.Errorf("%s: observed %v %q tool %q", tc.name, l.Observed, l.ObservedNote, l.CurrentRoute.Tool)
		}
		list := buildPanelList(dockReport{Loops: []dockLoop{l}})
		if len(list.Loops) != 1 || !list.Loops[0].Observed || slicesContain(list.CanAll, "stop") ||
			!strings.Contains(list.Loops[0].ObservedNote, "Pause and resume change only whether looptender checks it; nothing is ever sent") {
			t.Errorf("%s: panel list %+v", tc.name, list)
		}
	}
	// An ordinary target keeps stop.
	l, _ := projectDockLoop(dockEvidence{controls: &clientControls{Enabled: true}}.input(), dockNow, defaultStaleAfter, false)
	if !reflect.DeepEqual(l.Can, []string{"pause", "stop"}) || l.Observed {
		t.Errorf("an ordinary target: can %v observed %v", l.Can, l.Observed)
	}
	if r := refOf(config.Agent{Adapter: "claude", Claude: &config.Claude{}}); r.Observed {
		t.Error("a claude target reads as observed")
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// status prints that the target is observed and that no task is sent.
func TestStatusSaysAnObservedTargetIsNeverWoken(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "looptender.yaml")
	body := "version: 1\nloop:\n  name: example-observed\n  state_dir: state\n  adapter: sessionpipe\n  sessionpipe:\n    session: codex:01a10860-a43f-7ce2-b12f-a7911ae8a880\n    events_file: events.jsonl\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	run(context.Background(), []string{"status", "-c", cfg}, &out, &errOut)
	for _, want := range []string{"task:      none; an observed target is sent nothing", "observed:  Observed, never woken"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s\n%s", want, out.String(), errOut.String())
		}
	}
	out.Reset()
	run(context.Background(), []string{"status", "-c", cfg, "--json"}, &out, &errOut)
	var st statusOut
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !st.TargetAgent.Observed || st.TargetAgent.Session != "codex:01a10860-a43f-7ce2-b12f-a7911ae8a880" || st.TaskSource != "none" {
		t.Errorf("status JSON: %+v task %q", st.TargetAgent, st.TaskSource)
	}
}

// A wake_only observed loop surfaces observedWakes and sessionpipeWake in the
// dock and panel list, gives honest hold advice that resume does not send, and
// titles panel reviews as "Pause checking" / "Resume checking".
func TestDockObservedWakeHoldAdviceAndReviewWording(t *testing.T) {
	wakeUntil := dockNow.Add(time.Hour)
	for _, wakes := range []bool{false, true} {
		in := dockEvidence{
			controls: &clientControls{Enabled: true, AwaitingUser: true},
			status: func(s *statusOut) {
				send := ""
				if wakes {
					send = config.SessionpipeSendWakeOnly
					s.SessionpipeWake = &sessionpipeWakeStatus{
						Held: true, Outcome: "refused", Code: "folder", Until: &wakeUntil,
						Text: "last wake refused: folder, next attempt after 2030-01-01T01:00:00Z or the session's next turn; nothing is sent until then",
					}
				}
				s.TargetAgent = refOf(config.Agent{
					Adapter: config.SessionpipeAdapter, Provider: config.SessionpipeAdapter,
					Sessionpipe: &config.Sessionpipe{Session: "claude-code:example", EventsFile: "/example/events.jsonl", Send: send},
				})
				s.State.LastCheck = &state.Check{At: dockNow.Add(-time.Minute), Action: "await_user", Reason: "target gone (archived, deleted, or not found)", Outcome: "succeeded"}
			},
		}.input()
		l, need := projectDockLoop(in, dockNow, defaultStaleAfter, false)
		if !l.Observed || l.ObservedWakes != wakes {
			t.Fatalf("wakes=%v: observed=%v observedWakes=%v", wakes, l.Observed, l.ObservedWakes)
		}
		if wakes && (l.SessionpipeWake == nil || !l.SessionpipeWake.Held || l.SessionpipeWake.Code != "folder") {
			t.Fatalf("wakes=%v: sessionpipeWake=%+v", wakes, l.SessionpipeWake)
		}
		if !wakes && l.SessionpipeWake != nil {
			t.Fatalf("never-woken loop carried sessionpipeWake: %+v", l.SessionpipeWake)
		}
		if need == nil || need.Reason != "The observed session ended or was forgotten." {
			t.Fatalf("wakes=%v: gone need=%+v", wakes, need)
		}
		if !wakes && need.Action != "Resume continues checking; nothing is sent." {
			t.Fatalf("never-woken gone advice=%q", need.Action)
		}
		if wakes && need.Action != "Resume continues checking and idle wakes; nothing is sent by resuming." {
			t.Fatalf("wake_only gone advice=%q", need.Action)
		}
		list := buildPanelList(dockReport{Loops: []dockLoop{l}})
		if len(list.Loops) != 1 || list.Loops[0].ObservedWakes != wakes {
			t.Fatalf("wakes=%v: panelListLoop=%+v", wakes, list.Loops)
		}

		// Panel review wording for pause and resume.
		pl := panelLoop{dockLoop: l}
		pausePrev := panelPreviewOf("pause", pl, control.File{Enabled: true}, dockNow.Add(2*time.Minute), dockNow)
		if pausePrev.Title != "Pause checking "+l.Name+"?" || pausePrev.Confirm != "Pause checking "+l.Name {
			t.Fatalf("wakes=%v: pause preview=%+v", wakes, pausePrev)
		}
		resumePrev := panelPreviewOf("resume", pl, control.File{Enabled: true, AwaitingUser: true}, dockNow.Add(2*time.Minute), dockNow)
		if resumePrev.Title != "Resume checking "+l.Name+"?" || resumePrev.Confirm != "Resume checking "+l.Name {
			t.Fatalf("wakes=%v: resume preview=%+v", wakes, resumePrev)
		}
		joined := make([]string, len(resumePrev.Lines))
		for i, ln := range resumePrev.Lines {
			joined[i] = ln.render(dockNow)
		}
		linesText := strings.Join(joined, "\n")
		if !wakes && !strings.Contains(linesText, "Resume continues checking; nothing is ever sent to this session.") {
			t.Fatalf("never-woken resume preview missing honest line: %s", linesText)
		}
		if wakes && !strings.Contains(linesText, "Resume continues checking and idle wakes; nothing is sent by resuming") {
			t.Fatalf("wake_only resume preview missing honest line: %s", linesText)
		}
	}

	// Table check for other hold causes on an observed loop (input, note, generic).
	for _, tc := range []struct {
		name  string
		wakes bool
		cause string
		note  string
		want  string
	}{
		{"never-woken input", false, "input", "", "Answer it in the session, then resume checking; nothing is sent."},
		{"never-woken generic", false, "", "", "Look at the session, then resume checking when it is ready; nothing is sent."},
		{"never-woken note", false, "", "Please inspect session", "See the note left with the hold and the session. Resume continues checking; nothing is sent."},
		{"wake_only input", true, "input", "", "Answer it in the session, then resume checking; nothing is sent by resuming."},
		{"wake_only note", true, "", "Please inspect session", "See the note left with the hold and the session. Resume continues checking and idle wakes; nothing is sent by resuming."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &statusOut{
				TargetConfigured: true,
				TargetAgent:      agentRef{Observed: true, Wakes: tc.wakes},
				Control:          control.File{Enabled: true, AwaitingUser: true, Note: tc.note},
			}
			n := dockHoldNeed(st, &dockHold{Cause: tc.cause}, false)
			if n.Action != tc.want {
				t.Fatalf("got %q, want %q", n.Action, tc.want)
			}
		})
	}
}
