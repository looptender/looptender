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

	"github.com/looptender/looptender/internal/config"
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
