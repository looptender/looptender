//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/hostcheck"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/state"
)

func ctlOf(t *testing.T, f *fixture) control.File {
	t.Helper()
	c, err := control.Load(archiveFixtureDir(t, f))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tickAt(t *testing.T, f *fixture, at time.Time) {
	t.Helper()
	f.run("tick", "--now", at.UTC().Format(time.RFC3339))
}

func pinSession(t *testing.T, available bool) {
	t.Helper()
	r := hostcheck.Fixed()
	if !available {
		r.Keychain = func(context.Context) error { return errors.New("fictional: keychain unreachable") }
		r.Console = func() (bool, bool) { return false, true }
	}
	hostcheck.PinForTests(r)
	t.Cleanup(func() { hostcheck.PinForTests(hostcheck.Fixed()) })
}

// A wait sends nothing until its condition holds, then carries on by
// itself: the control record is again exactly what it was before.
func TestWaitForATimeCarriesOnByItself(t *testing.T) {
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	before := ctlOf(t, f)
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	code, out, errs := f.run("wait", "--until", "time:"+at.Format(time.RFC3339), "--note", "fictional release window")
	if code != exitOK || !strings.Contains(out, "waiting for a time ("+at.Format(time.RFC3339)+")") {
		t.Fatalf("wait: %d %s %s", code, out, errs)
	}
	if c := ctlOf(t, f); !c.AwaitingUser {
		t.Fatal("no hold while waiting")
	}
	_, text, _ := f.run("status")
	if !strings.Contains(text, "waiting:   waiting for a time") {
		t.Fatalf("status: %s", text)
	}
	tickAt(t, f, time.Now())
	if c := ctlOf(t, f); !c.AwaitingUser {
		t.Fatal("the wait ended before its time")
	}
	tickAt(t, f, at.Add(time.Minute))
	c := ctlOf(t, f)
	if c.AwaitingUser || c.Note != before.Note || !c.Enabled {
		t.Fatalf("not restored: %+v", c)
	}
	if h, _ := control.LoadSelfHold(archiveFixtureDir(t, f), control.WaitFileName); h != nil {
		t.Fatal("wait record kept")
	}
	runs, _ := os.ReadFile(filepath.Join(archiveFixtureDir(t, f), state.LogName))
	if !strings.Contains(string(runs), `"action":"wait_cleared"`) {
		t.Fatalf("runs.jsonl: %s", runs)
	}
}

// A person's hold under a wait stays when the wait ends; a wait past its
// time becomes an ordinary await-user with its reason; a person changing
// the control record ends the wait as a wait.
func TestWaitKeepsAPersonsHoldExpiresAndYieldsToAPerson(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{{"enable"}, {"await-user", "--note", "fictional question for a person"}} {
		if code, _, errs := f.run(args...); code != exitOK {
			t.Fatal(errs)
		}
	}
	pinSession(t, true)
	if code, _, errs := f.run("wait", "--until", "host-session"); code != exitOK {
		t.Fatal(errs)
	}
	f.tick()
	if c := ctlOf(t, f); !c.AwaitingUser || c.Note != "fictional question for a person" {
		t.Fatalf("a person's hold was lifted: %+v", c)
	}

	g := newFixture(t)
	if code, _, errs := g.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	pinSession(t, false)
	if code, _, errs := g.run("wait", "--until", "host-session", "--for", "1m", "--note", "fictional outage"); code != exitOK {
		t.Fatal(errs)
	}
	g.tick()
	if c := ctlOf(t, g); !c.AwaitingUser {
		t.Fatal("cleared while the session is unavailable")
	}
	tickAt(t, g, time.Now().Add(2*time.Minute))
	if c := ctlOf(t, g); !c.AwaitingUser || !strings.Contains(c.Note, "for host-session, which did not come (fictional outage)") {
		t.Fatalf("expiry: %+v", c)
	}
	if h, _ := control.LoadSelfHold(archiveFixtureDir(t, g), control.WaitFileName); h != nil {
		t.Fatal("expired wait kept")
	}

	k := newFixture(t)
	if code, _, errs := k.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := k.run("wait", "--until", "host-session"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := k.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	pinSession(t, true)
	k.tick()
	if c := ctlOf(t, k); !c.Paused || !c.AwaitingUser {
		t.Fatalf("a wait ended a person's change: %+v", c)
	}
	if h, _ := control.LoadSelfHold(archiveFixtureDir(t, k), control.WaitFileName); h != nil {
		t.Fatal("stale wait kept")
	}
}

func TestWaitRefusals(t *testing.T) {
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	far := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"wait", "--until", "the-agent-says-so"}, "unknown condition"},
		{[]string{"wait", "--until", "time:" + past}, "already passed"},
		{[]string{"wait", "--until", "time:" + far}, "use --for"},
		{[]string{"wait", "--until", "limit-reset"}, "no future usage-limit reset"},
		{[]string{"wait", "--until", "host-session", "--for", "200h"}, "--for must be"},
		{[]string{"pause", "--until", "host-session"}, "only supported by wait"},
	} {
		if code, _, errs := f.run(c.args...); code == exitOK || !strings.Contains(errs, c.want) {
			t.Errorf("%v: %d %s", c.args, code, errs)
		}
	}
	if c := ctlOf(t, f); c.AwaitingUser {
		t.Fatal("a refused wait changed control")
	}
}

// A build from before waits reads the hold as an ordinary await-user and
// sends nothing; when it resumes the loop, the wait is dropped, never
// restored over that resume.
func TestOlderBuildSeesAWaitAsAHold(t *testing.T) {
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("wait", "--until", "host-session"); code != exitOK {
		t.Fatal(errs)
	}
	dir := archiveFixtureDir(t, f)
	raw, _ := os.ReadFile(filepath.Join(dir, control.FileName))
	var older map[string]any
	if json.Unmarshal(raw, &older) != nil || older["awaitingUser"] != true || strings.Contains(string(raw), "wait.json") {
		t.Fatalf("older reader: %s", raw)
	}
	older["awaitingUser"], older["note"] = false, ""
	b, _ := json.Marshal(older)
	if err := os.WriteFile(filepath.Join(dir, control.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	pinSession(t, true)
	f.tick()
	if c := ctlOf(t, f); c.AwaitingUser {
		t.Fatalf("restored over an older build's resume: %+v", c)
	}
	if h, _ := control.LoadSelfHold(dir, control.WaitFileName); h != nil {
		t.Fatal("stale wait kept")
	}
}

// The page and the list: a wait needs nobody and has its own kind.
func TestDockShowsAWaitAsItsOwnState(t *testing.T) {
	in := dockEvidence{controls: &clientControls{Enabled: true, AwaitingUser: true}}.input()
	in.Client.Snapshot.Wait = &waitView{Until: "host-session", Since: dockNow.Add(-time.Hour), Text: "waiting for the login session"}
	r := buildDockReport([]dockInput{in}, dockNow, 15*time.Second, 30*time.Minute, false, true, 0, true)
	l := r.Loops[0]
	if l.State != "waiting_on_condition" || l.Waiting == nil || l.Waiting.Kind != "condition" || len(r.NeedsYou) != 0 {
		t.Fatalf("%+v %+v %+v", l.State, l.Waiting, r.NeedsYou)
	}
	if row := buildPanelList(r).Loops[0]; row.NeedsYou || row.Waiting == nil || row.Waiting.Kind != "condition" {
		t.Fatalf("list: %+v", row)
	}
}

func ownedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	return f
}

// The owner thread takes back the question it set, and the history and
// status say the session withdrew it.
func TestOwnerThreadWithdrawsItsOwnQuestion(t *testing.T) {
	f := ownedFixture(t)
	before := ctlOf(t, f)
	if code, _, errs := f.run("await-user", "--note", "fictional question"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("await-user", "--withdraw"); code != exitUsage || !strings.Contains(errs, "needs --note") {
		t.Fatalf("no note: %d %s", code, errs)
	}
	code, out, errs := f.run("await-user", "--withdraw", "--note", "found the answer in the fictional docs")
	if code != exitOK || !strings.Contains(out, "the session withdrew its own question") {
		t.Fatalf("withdraw: %d %s %s", code, out, errs)
	}
	if c := ctlOf(t, f); c.AwaitingUser || c.Note != before.Note {
		t.Fatalf("not restored: %+v", c)
	}
	if _, text, _ := f.run("status"); !strings.Contains(text, "question:  the session withdrew its own question: found the answer in the fictional docs") {
		t.Fatalf("status: %s", text)
	}
	j, _ := incident.Load(archiveFixtureDir(t, f))
	last := j.Records[len(j.Records)-1]
	if last.ControlChange == nil || last.ControlChange.Operation != "await-user_withdrawn" || last.ControlChange.Caller != "owner_thread" {
		t.Fatalf("journal: %+v", last.ControlChange)
	}
	// Once only.
	if code, _, _ := f.run("await-user", "--withdraw", "--note", "again"); code != exitFailed {
		t.Fatal("withdrew twice")
	}
}

// A person's hold can never become the session's to withdraw: re-running
// await-user on top of it and withdrawing leaves the person's hold.
func TestWithdrawCannotLaunderAPersonsHold(t *testing.T) {
	f := ownedFixture(t)
	t.Setenv("BB_THREAD_ID", "")
	if code, _, errs := f.run("await-user", "--note", "a person's fictional question"); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	if code, _, errs := f.run("await-user", "--withdraw", "--note", "x"); code != exitFailed || !strings.Contains(errs, "no await-user set by this loop's own thread") {
		t.Fatalf("withdrew a person's hold: %d %s", code, errs)
	}
	if code, _, errs := f.run("await-user", "--note", "the session's own words"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("await-user", "--withdraw", "--note", "x"); code != exitOK {
		t.Fatal(errs)
	}
	if c := ctlOf(t, f); !c.AwaitingUser || c.Note != "a person's fictional question" {
		t.Fatalf("the person's hold did not stay: %+v", c)
	}
}

func TestWithdrawRefusals(t *testing.T) {
	// A change by anyone since.
	f := ownedFixture(t)
	if code, _, errs := f.run("await-user", "--note", "q"); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "")
	if code, _, errs := f.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	if code, _, errs := f.run("await-user", "--withdraw", "--note", "x"); code != exitFailed || !strings.Contains(errs, "changed after the question was set") {
		t.Fatalf("after a person's pause: %d %s", code, errs)
	}
	// Another thread.
	g := ownedFixture(t)
	if code, _, errs := g.run("await-user", "--note", "q"); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleother")
	if code, _, errs := g.run("await-user", "--withdraw", "--note", "x"); code != exitFailed || !strings.Contains(errs, "only the thread that set the question") {
		t.Fatalf("another thread: %d %s", code, errs)
	}
	// History that no longer shows it.
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	if err := os.Remove(filepath.Join(archiveFixtureDir(t, g), incident.FileName)); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := g.run("await-user", "--withdraw", "--note", "x"); code != exitFailed || !strings.Contains(errs, "history") {
		t.Fatalf("no history: %d %s", code, errs)
	}
	// Archived.
	k := ownedFixture(t)
	if code, _, errs := k.run("await-user", "--note", "q"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := k.run("archive"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := k.run("await-user", "--withdraw", "--note", "x"); code != exitFailed || !strings.Contains(errs, "archived") {
		t.Fatalf("archived: %d %s", code, errs)
	}
}
