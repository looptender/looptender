//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// successionWorld is the fixture's loop as the successor and a second loop,
// enabled and held, as its predecessor.
func successionWorld(t *testing.T) (*fixture, string, *config.Config, *config.Config) {
	t.Helper()
	f := newFixture(t)
	old := secondLoop(t, f, "example-old", nil)
	for _, args := range [][]string{{"enable", "-c", old, "--allow-second-loop"}, {"pause", "-c", old}, {"display-name", "-c", old, "Example line of work"}} {
		if code, _, errs := runWith(args...); code != exitOK {
			t.Fatalf("%v: %s", args, errs)
		}
	}
	if code, _, errs := f.run("enable", "--allow-second-loop"); code != exitOK {
		t.Fatal(errs)
	}
	oldCfg, err := config.Load(old)
	if err != nil {
		t.Fatal(err)
	}
	newCfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f, old, oldCfg, newCfg
}

// A declared succession names the successor by the line of work's name at
// once, archives the predecessor only after the successor's first completed
// check, and records each in the other's status.
func TestDeclaredSuccessionArchivesThePredecessorAfterTheFirstCheck(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	code, out, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg)
	if code != exitOK || !strings.Contains(out, "Recorded: loop example-loop continues loop example-old") {
		t.Fatalf("declare: %d %s %s", code, out, errs)
	}
	if a, _ := control.LoadArchive(oldCfg.Loop.StateDir); a != nil {
		t.Fatal("archived before the successor's check")
	}
	if n := namingOf(newCfg.Loop, ""); n.DisplayName != "Example line of work" || n.NameSource != state.DisplayNameInherited {
		t.Fatalf("name: %+v", n)
	}
	// A check that completes: the predecessor is archived, naming its successor.
	if r := f.tick(); r["outcome"] == "failed" {
		t.Fatalf("tick: %v", r)
	}
	a, _ := control.LoadArchive(oldCfg.Loop.StateDir)
	if a == nil || a.ContinuedIn == nil || a.ContinuedIn.Loop != "example-loop" || !strings.Contains(a.Note, "continued in loop example-loop") || a.Hold != "paused" {
		t.Fatalf("archive: %+v", a)
	}
	if ctl, _ := control.Load(oldCfg.Loop.StateDir); !ctl.Paused {
		t.Fatal("the predecessor's hold changed")
	}
	_, js, _ := f.run("status", "--json")
	var st struct {
		ContinuedFrom *continuedFromView `json:"continuedFrom"`
		Lineage       []lineageStep      `json:"lineage"`
		Naming        loopNaming         `json:"naming"`
	}
	if json.Unmarshal([]byte(js), &st) != nil || st.ContinuedFrom == nil || st.ContinuedFrom.Loop != "example-old" || st.ContinuedFrom.ArchivedAt == nil || len(st.Lineage) != 1 || st.Naming.DisplayName != "Example line of work" {
		t.Fatalf("successor status: %s", js)
	}
	if _, text, _ := runWith("status", "-c", old); !strings.Contains(text, "continued in: loop example-loop") {
		t.Fatalf("predecessor status: %s", text)
	}
	// A later title read never renames the line of work.
	if err := state.NoteThreadTitle(newCfg.Loop.StateDir, "thr_primary", "Something else", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := namingOf(newCfg.Loop, ""); n.DisplayName != "Example line of work" {
		t.Fatalf("renamed: %+v", n)
	}
}

// A predecessor that still supervises is not archived: the record waits and
// says why, and neither loop is hidden. A thread that does not own the
// predecessor cannot declare a succession.
func TestSuccessionWaitsForAHeldPredecessorAndNeedsItsOwner(t *testing.T) {
	f, old, oldCfg, _ := successionWorld(t)
	if code, _, errs := runWith("resume", "-c", old); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	f.tick()
	if a, _ := control.LoadArchive(oldCfg.Loop.StateDir); a != nil {
		t.Fatal("a supervising predecessor was archived")
	}
	_, text, _ := f.run("status")
	if !strings.Contains(text, "continues: loop example-old") || !strings.Contains(text, "not archived yet: the predecessor is still supervising") {
		t.Fatalf("status: %s", text)
	}
	// Owned by a thread: another thread may not declare.
	g := newFixture(t)
	older := secondLoop(t, g, "example-older", nil)
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	for _, args := range [][]string{{"enable", "-c", older, "--allow-second-loop"}, {"pause", "-c", older}} {
		if code, _, errs := runWith(args...); code != exitOK {
			t.Fatalf("%v: %s", args, errs)
		}
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleother")
	if code, _, errs := runWith("archive", "-c", older, "--succeeded-by", g.cfg); code != exitFailed || !strings.Contains(errs, "different thread from the predecessor's owner") {
		t.Fatalf("declare by another thread: %d %s", code, errs)
	}
}

// transfer supersede records the succession on the one registered loop that
// supervises the successor thread.
func TestSupersedeRecordsTheSuccessorLoop(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	dir := os.Getenv(registryDirEnv)
	for _, c := range []string{old, f.cfg} {
		if code, out, errs := registryRun(t, dir, "add", "-c", c); code != exitOK && !strings.Contains(out+errs, "already") {
			t.Fatal(out, errs)
		}
	}
	var out bytes.Buffer
	supersedeSuccession(env{context.Background(), &out, &out}, oldCfg, "thr_primary", time.Now())
	s, err := control.LoadSuccession(newCfg.Loop.StateDir)
	if err != nil || s == nil || s.Source != "supersede" || s.From.Loop != "example-old" || s.From.RegistryID == "" {
		t.Fatalf("%+v %v %s", s, err, out.String())
	}
}

// Lists carry a count and the predecessor's key; an archived predecessor
// names its successor by key only.
func TestListsCarryOnlyTheLineageCountAndKey(t *testing.T) {
	at := dockNow.Add(-time.Hour)
	succ := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	succ.Client.Snapshot.Lineage = &listLineage{Count: 3, From: "l0123456789ab"}
	pred := dockEvidence{controls: &clientControls{Enabled: true, Paused: true}}.input()
	pred.Entry.ID, pred.Entry.Loop = "loop_0123456789abcdef0123456789abcde2", "example-old"
	pred.Client.Snapshot.Archived = &archiveView{At: at, Hold: "paused", ContinuedIn: &continuedRef{Loop: "example-loop", Thread: "thr_example1", Key: "l0123456789ab", At: &at}}
	list := buildPanelList(buildDockReport([]dockInput{succ, pred}, dockNow, 15*time.Second, 30*time.Minute, false, true, 0, true))
	b, _ := json.Marshal(list)
	if !strings.Contains(string(b), `"lineage":{"count":3,"from":"l0123456789ab"}`) || !strings.Contains(string(b), `"continuedIn":{"key":"l0123456789ab"}`) || strings.Contains(string(b), "thr_") {
		t.Fatalf("%s", b)
	}
}

func successionOf(t *testing.T, cfg *config.Config) *control.Succession {
	t.Helper()
	s, err := control.LoadSuccession(cfg.Loop.StateDir)
	if err != nil || s == nil {
		t.Fatalf("record: %+v %v", s, err)
	}
	return s
}

func notArchived(t *testing.T, c *config.Config) {
	t.Helper()
	if a, _ := control.LoadArchive(c.Loop.StateDir); a != nil {
		t.Fatalf("archived: %+v", a)
	}
}

// A record the predecessor never agreed to (no mark in its own directory)
// archives nothing, and is cancelled.
func TestForgedSuccessionRecordArchivesNothing(t *testing.T) {
	f, _, oldCfg, newCfg := successionWorld(t)
	if err := control.SaveSuccession(newCfg.Loop.StateDir, control.Succession{From: control.LoopRef{Loop: "example-old", StateDir: oldCfg.Loop.StateDir, At: time.Now()},
		Source: "declared", By: "person", DeclaredAt: time.Now().Add(-time.Minute), FromHeld: true}); err != nil {
		t.Fatal(err)
	}
	f.tick()
	notArchived(t, oldCfg)
	if s := successionOf(t, newCfg); s.CancelledAt == nil || !strings.Contains(s.Cancelled, "no matching mark") {
		t.Fatalf("%+v", s)
	}
}

// The predecessor's owner changing after the declaration cancels it.
func TestSuccessionIsCancelledWhenThePredecessorsOwnerChanges(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_examplenewowner")
	if code, _, errs := runWith("enable", "-c", old, "--take-over", "--allow-second-loop"); code != exitOK {
		t.Fatal(errs)
	}
	t.Setenv("BB_THREAD_ID", "")
	f.tick()
	notArchived(t, oldCfg)
	if s := successionOf(t, newCfg); s.CancelledAt == nil || !strings.Contains(s.Cancelled, "owner changed") {
		t.Fatalf("%+v", s)
	}
}

// A person who resumes the predecessor after declaring has decided not to
// switch: holding it again later archives nothing; the succession is
// cancelled and status says so.
func TestSuccessionIsCancelledWhenThePredecessorIsResumed(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	for _, c := range []string{"resume", "pause"} {
		if code, _, errs := runWith(c, "-c", old); code != exitOK {
			t.Fatal(errs)
		}
	}
	f.tick()
	notArchived(t, oldCfg)
	if s := successionOf(t, newCfg); s.CancelledAt == nil || !strings.Contains(s.Cancelled, "resumed or held again") {
		t.Fatalf("%+v", s)
	}
	if _, text, _ := f.run("status"); !strings.Contains(text, "continues: loop example-old") || !strings.Contains(text, ", cancelled ") {
		t.Fatalf("status: %s", text)
	}
	_, js, _ := f.run("status", "--json")
	if strings.Contains(js, `"lineage"`) {
		t.Fatalf("a cancelled succession is in the lineage: %s", js)
	}
}

// A succession that waits longer than its bound is cancelled.
func TestPendingSuccessionExpires(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	if code, _, errs := runWith("resume", "-c", old); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	s := successionOf(t, newCfg)
	s.DeclaredAt = s.DeclaredAt.Add(-successionPendingMax - time.Hour)
	if err := control.SaveSuccession(newCfg.Loop.StateDir, *s); err != nil {
		t.Fatal(err)
	}
	f.tick()
	notArchived(t, oldCfg)
	if s := successionOf(t, newCfg); s.CancelledAt == nil || !strings.Contains(s.Cancelled, "within") {
		t.Fatalf("%+v", s)
	}
}

// No cycle, and one successor per loop.
func TestSuccessionRefusesACycleAndASecondSuccessor(t *testing.T) {
	f, old, oldCfg, _ := successionWorld(t)
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := runWith("archive", "-c", f.cfg, "--succeeded-by", old); code != exitFailed || !strings.Contains(errs, "already an earlier loop") {
		t.Fatalf("cycle: %d %s", code, errs)
	}
	if code, _, errs := f.run("resume"); code != exitOK {
		t.Fatal(errs)
	}
	f.tick()
	if a, _ := control.LoadArchive(oldCfg.Loop.StateDir); a == nil || a.ContinuedIn == nil {
		t.Fatal("not archived")
	}
	third := secondLoop(t, f, "example-third", nil)
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", third); code != exitFailed || !strings.Contains(errs, "already continued in loop example-loop") {
		t.Fatalf("second successor: %d %s", code, errs)
	}
	// And the archive itself refuses another successor.
	if _, _, err := control.ArchiveLoopFor(oldCfg.Loop.StateDir, "", time.Now(), nil, &control.LoopRef{Loop: "example-third", StateDir: "/fictional/other"}); err != control.ErrContinuedElsewhere {
		t.Fatalf("re-archive for another successor: %v", err)
	}
}

func TestAutoInheritedNameFollowsSuccessorTitle(t *testing.T) {
	f, old, oldCfg, newCfg := successionWorld(t)
	now := time.Now().UTC()
	if err := state.SaveDisplayName(oldCfg.Loop.StateDir, "Example task (Claude)", state.DisplayNameThreadTitle, now); err != nil {
		t.Fatal(err)
	}
	if err := state.NoteThreadTitle(oldCfg.Loop.StateDir, bbThreadOf(oldCfg), "Example task (Claude)", nil, now); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runWith("archive", "-c", old, "--succeeded-by", f.cfg); code != exitOK {
		t.Fatal(errs)
	}
	if n := namingOf(newCfg.Loop, "thr_primary"); n.DisplayName != "Example task" || n.NameSource != state.DisplayNameInheritedTitle {
		t.Fatalf("inherited %+v", n)
	}
	if err := state.NoteThreadTitle(newCfg.Loop.StateDir, "thr_primary", "Successor task (Codex)", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := namingOf(newCfg.Loop, "thr_primary"); n.DisplayName != "Successor task (Codex)" || n.NameSource != state.DisplayNameThreadTitle {
		t.Fatalf("successor %+v", n)
	}
	// The previous thread's model label must not be presented on a new target.
	if n := namingOf(newCfg.Loop, "thr_another"); n.DisplayName != "Example task" || n.NameSource != state.DisplayNameInheritedTitle || n.ThreadTitle != "" {
		t.Fatalf("wrong target %+v", n)
	}
}
