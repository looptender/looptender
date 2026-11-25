//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

func archiveFixtureDir(t *testing.T, f *fixture) string {
	t.Helper()
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Loop.StateDir
}

// Archive is for a loop that is not supervising, changes no hold, blocks
// resume, enable and start until unarchive, which resumes nothing.
func TestArchiveCommandsKeepHoldsAndBlockResume(t *testing.T) {
	f := newFixture(t)
	dir := archiveFixtureDir(t, f)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	code, out, _ := f.run("archive")
	if code != exitFailed || !strings.Contains(out, "archive refused, nothing written") || !strings.Contains(out, "is supervising. Pause or stop it first") {
		t.Fatalf("archive of a supervising loop: %d %s", code, out)
	}
	if code, _, errs := f.run("await-user", "--note", "fictional reason"); code != exitOK {
		t.Fatal(errs)
	}
	before, _ := os.ReadFile(filepath.Join(dir, control.FileName))
	stBefore, _ := os.ReadFile(filepath.Join(dir, state.FileName))
	code, out, errs := f.run("archive", "--note", "continued elsewhere")
	if code != exitOK || !strings.Contains(out, "Archived loop example-loop (it is awaiting user)") || !strings.Contains(out, "resumes nothing") {
		t.Fatalf("archive: %d %s %s", code, out, errs)
	}
	after, _ := os.ReadFile(filepath.Join(dir, control.FileName))
	if !bytes.Equal(before, after) {
		t.Fatalf("control.json changed:\n%s\n%s", before, after)
	}
	if stAfter, _ := os.ReadFile(filepath.Join(dir, state.FileName)); !bytes.Equal(stBefore, stAfter) {
		t.Fatal("state.json changed")
	}
	for _, cmd := range []string{"resume", "enable"} {
		code, _, errs := f.run(cmd)
		if code != exitFailed || !strings.Contains(errs, cmd+" refused, nothing changed: loop example-loop is archived since ") || !strings.Contains(errs, "looptender unarchive -c ") {
			t.Fatalf("%s while archived: %d %s", cmd, code, errs)
		}
	}
	if code, _, errs := runWith("start", "--foreground", "-c", f.cfg); code != exitFailed || !strings.Contains(errs, "start refused, nothing changed: loop example-loop is archived") {
		t.Fatalf("start while archived: %d %s", code, errs)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, control.FileName)); !bytes.Equal(before, got) {
		t.Fatal("a refused command changed control.json")
	}
	// A tick checks nothing and records nothing.
	if code, _, errs := f.run("tick"); code != exitOK || !strings.Contains(errs, "loop example-loop is archived; nothing checked") {
		t.Fatalf("tick: %d %s", code, errs)
	}
	if stAfter, _ := os.ReadFile(filepath.Join(dir, state.FileName)); !bytes.Equal(stBefore, stAfter) {
		t.Fatal("a tick of an archived loop wrote state.json")
	}
	// Status, text and JSON; doctor.
	_, text, _ := f.run("status")
	if !strings.Contains(text, "archived:  since ") || !strings.Contains(text, "when it was awaiting user (continued elsewhere)") || !strings.Contains(text, "Resume and enable are refused until") {
		t.Fatalf("status: %s", text)
	}
	_, js, _ := f.run("status", "--json")
	var st struct {
		Archived      *archiveView `json:"archived"`
		ArchiveLapsed *archiveView `json:"archiveLapsed"`
		Naming        loopNaming   `json:"naming"`
	}
	if err := json.Unmarshal([]byte(js), &st); err != nil || st.Archived == nil || st.Archived.Note != "continued elsewhere" || st.Archived.Hold != "awaiting user" || st.ArchiveLapsed != nil || st.Naming.Slug != "example-loop" {
		t.Fatalf("status json: %v %s", err, js)
	}
	if _, r := doctorOfFixture(t, f); r.Archived == nil || !strings.Contains(doctorText(r), "loop.archived") {
		t.Fatalf("doctor: %+v", r)
	}
	// Unarchive resumes nothing; then resume works.
	code, out, _ = f.run("unarchive")
	if code != exitOK || !strings.Contains(out, "Nothing was resumed: it is still awaiting user") {
		t.Fatalf("unarchive: %d %s", code, out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, control.FileName)); !bytes.Equal(before, got) {
		t.Fatal("unarchive changed control.json")
	}
	if code, out, _ := f.run("unarchive"); code != exitOK || !strings.Contains(out, "is not archived; nothing changed") {
		t.Fatalf("second unarchive: %d %s", code, out)
	}
	if code, _, errs := f.run("resume"); code != exitOK {
		t.Fatalf("resume after unarchive: %s", errs)
	}
	runs, _ := os.ReadFile(filepath.Join(dir, state.LogName))
	if !strings.Contains(string(runs), `"action":"archive"`) || !strings.Contains(string(runs), `"action":"unarchive"`) {
		t.Fatalf("runs.jsonl: %s", runs)
	}
}

func doctorOfFixture(t *testing.T, f *fixture) (int, doctorReport) {
	t.Helper()
	code, out, errs := f.run("doctor", "--offline", "--json")
	var r doctorReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("doctor: %d %v %s %s", code, err, out, errs)
	}
	return code, r
}

func doctorText(r doctorReport) string {
	var b strings.Builder
	for _, c := range r.Checks {
		b.WriteString(c.ID + " " + c.Status + " " + c.Message + "\n")
	}
	return b.String()
}

// A daemon of an archived loop exits cleanly, recording why, so a service
// manager that restarts only failed daemons leaves it stopped. The same
// holds after a build switch, whose new build runs this loop from its first
// pass.
func TestDaemonOfAnArchivedLoopExitsCleanly(t *testing.T) {
	f := newFixture(t)
	dir := archiveFixtureDir(t, f)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("archive"); code != exitOK {
		t.Fatal(errs)
	}
	done := make(chan int, 1)
	var errb bytes.Buffer
	go func() {
		var out bytes.Buffer
		done <- run(context.Background(), []string{"daemon", "-c", f.cfg}, &out, &errb)
	}()
	select {
	case code := <-done:
		if code != exitOK || !strings.Contains(errb.String(), "is archived (since ") || !strings.Contains(errb.String(), "this daemon checks nothing more and exits") {
			t.Fatalf("daemon: %d %s", code, errb.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon of an archived loop kept running")
	}
	runs, _ := os.ReadFile(filepath.Join(dir, state.LogName))
	if !strings.Contains(string(runs), `"reason":"archived"`) {
		t.Fatalf("runs.jsonl: %s", runs)
	}
	// The adoption path: a daemon does not hand an archived loop to a new
	// build (it exits instead).
	a := newBuildAdopter(f.cfg, dir, "daemon", "", nil)
	if r := a.loopReady(nil); r != "the loop is archived" {
		t.Fatalf("loopReady: %q", r)
	}
}

// What a build from before archiving sees, and what happens when it resumes
// an archived loop: control.json alone, which archiving never touched, says
// the loop is held; after such a resume the record lapses and is shown as
// such, never as archived.
func TestOlderBuildReadsAnArchivedLoopAsHeld(t *testing.T) {
	f := newFixture(t)
	dir := archiveFixtureDir(t, f)
	for _, c := range []string{"enable", "pause", "archive"} {
		if code, _, errs := f.run(c); code != exitOK {
			t.Fatalf("%s: %s", c, errs)
		}
	}
	// The older reader: control.json decoded with only the fields of state
	// format 6, which have not changed (TestAdoptStateFormatFollowsTheSchemas).
	raw, _ := os.ReadFile(filepath.Join(dir, control.FileName))
	var older struct {
		Enabled, Paused, Stop, AwaitingUser bool
	}
	if err := json.Unmarshal(raw, &older); err != nil || !older.Enabled || !older.Paused {
		t.Fatalf("older reader: %v %+v", err, older)
	}
	if strings.Contains(string(raw), "archiv") {
		t.Fatalf("control.json mentions the archive: %s", raw)
	}
	// An older build's resume: control.json rewritten from its own fields.
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["paused"] = false
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, control.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, js, _ := f.run("status", "--json")
	var st struct {
		Archived      *archiveView `json:"archived"`
		ArchiveLapsed *archiveView `json:"archiveLapsed"`
	}
	if json.Unmarshal([]byte(js), &st) != nil || st.Archived != nil || st.ArchiveLapsed == nil {
		t.Fatalf("lapsed: %s", js)
	}
	if _, text, _ := f.run("status"); !strings.Contains(text, "archive:   the record since ") || !strings.Contains(text, "is ignored: the loop supervises again") {
		t.Fatalf("status: %s", text)
	}
}

// The page and the list: an archived loop is last, needs nobody, offers no
// control, is not worth a line, and is counted apart.
func TestDockAndPanelListShowArchivedLoopsApart(t *testing.T) {
	at := dockNow.Add(-time.Hour)
	held := dockEvidence{controls: &clientControls{Enabled: true, AwaitingUser: true}}.input()
	held.Client.Snapshot.Archived = &archiveView{At: at, Hold: "awaiting user"}
	held.Status.Archived = &archiveView{At: at, Hold: "awaiting user", Note: "see /Users/example/notes"}
	live := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	live.Entry.ID, live.Entry.Loop = "loop_0123456789abcdef0123456789abcde2", "example-live"
	r := buildDockReport([]dockInput{held, live}, dockNow, 15*time.Second, 30*time.Minute, false, true, 0, true)
	if len(r.NeedsYou) != 0 || r.Archived != 1 || !r.AllClear || !strings.Contains(r.Lede, "1 loop, 1 supervising, and 1 archived") {
		t.Fatalf("report: needs %+v archived %d lede %q", r.NeedsYou, r.Archived, r.Lede)
	}
	last := r.Loops[len(r.Loops)-1]
	if last.State != "archived" || last.Phrase != "Archived" || len(last.Can) != 0 || last.Archived == nil || strings.Contains(last.Archived.Note, "/Users/") {
		t.Fatalf("archived loop: %+v", last)
	}
	for _, w := range r.Worth {
		if w.Loop == "example-loop" {
			t.Fatalf("archived loop worth a line: %+v", w)
		}
	}
	list := buildPanelList(r)
	if list.Archived != 1 || list.NeedsYou != 0 || list.Loops[len(list.Loops)-1].Archived == nil || list.Loops[0].Name != "example-live" {
		t.Fatalf("list: %+v", list)
	}
	for _, a := range list.CanAll {
		_ = a // computed from the loops that can: the archived one offers nothing
	}
}

// shared: never calls an archived loop enabled or supervising.
func TestSharedWarningSaysArchived(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run("enable"); code != exitOK {
		t.Fatal("enable")
	}
	other := secondLoop(t, f, "example-loop-b", nil)
	if code, _, errs := runWith("enable", "-c", other, "--allow-second-loop"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("archive"); code != exitOK {
		t.Fatal(errs)
	}
	_, out, _ := runWith("status", "-c", other)
	if !strings.Contains(out, "is archived (held: paused): it sends nothing and cannot be resumed until it is unarchived") || strings.Contains(out, "example-loop ("+f.cfg+") is enabled") {
		t.Fatalf("status: %s", out)
	}
}

// One display name, made in one place; cut at the end.
func TestDisplayNameRuleAndStoredName(t *testing.T) {
	for in, want := range map[string]string{
		"looptender-coordinator":                        "looptender-coordinator",
		"looptender-coordinator-claude-3-with-a-suffix": "looptender-coordinator-claude…",
		"abcdefghijklmnopqrstuvwxyzabcdefghij":          "abcdefghijklmnopqrstuvwxyzabcde…",
	} {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
	dir := t.TempDir()
	l := config.Loop{Name: "example-loop", StateDir: dir, Progress: config.Progress{Repositories: []string{"/fictional/src/example-project"}}}
	if n := namingOf(l, "thr_example1"); n.DisplayName != "example-loop" || n.NameSource != "slug" || n.Project != "example-project" {
		t.Fatalf("%+v", n)
	}
	now := time.Now()
	// The first title read becomes the name; a later one does not change it.
	if err := state.NoteThreadTitle(dir, "thr_example1", "Example work · Sonnet medium", []string{"claude-code"}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.NoteThreadTitle(dir, "thr_example1", "Renamed in BB", nil, now); err != nil {
		t.Fatal(err)
	}
	n := namingOf(l, "thr_example1")
	if n.DisplayName != "Example work" || n.NameSource != state.DisplayNameTitleTrimmed || !n.TitleDiffers || n.ThreadTitle != "Renamed in BB" || n.Slug != "example-loop" {
		t.Fatalf("%+v", n)
	}
	if w := n.withoutPrivate(); w.ThreadTitle != "" || w.DisplayName != "Example work" {
		t.Fatalf("%+v", w)
	}
	// A person sets it; paths are hidden.
	if err := state.SaveDisplayName(dir, "Coordinator for /Users/example/x", state.DisplayNameSet, now); err != nil {
		t.Fatal(err)
	}
	if n := namingOf(l, ""); n.NameSource != state.DisplayNameSet || strings.Contains(n.DisplayName, "/Users/") || !strings.HasPrefix(n.DisplayName, "Coordinator for ") {
		t.Fatalf("%+v", n)
	}
}

// A group stop leaves an archived loop as it is and says so; a client resume
// is refused before any ticket; client status carries the record, with its
// note only in private mode.
func TestGroupChangesAndClientsLeaveArchivedLoopsAlone(t *testing.T) {
	d, byName := clientReviewWorld(t)
	var e registry.Entry
	for _, v := range byName {
		e = v
		break
	}
	if r, code := applyClientHold(context.Background(), d.directory, e.ID, "pause"); code != 0 || r.ControlSave != "confirmed" {
		t.Fatalf("%+v", r)
	}
	if _, _, err := control.ArchiveLoop(e.StateDir, "fictional note", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	g, refused := panelGroupReview(context.Background(), d.directory, d, "stop", dockNow)
	if refused != nil {
		t.Fatalf("%+v", refused)
	}
	var text []string
	for _, l := range g.preview.Lines {
		text = append(text, l.render(dockNow))
	}
	all := strings.Join(text, "\n")
	if !strings.Contains(all, "Stops 9 of 10 loops; 1 is archived and left as it is.") || panelGroupItemLine(panelGroupItem{Name: e.Loop, why: "archived"}, "", "stop") != e.Loop+" is archived and is left as it is." {
		t.Fatalf("review:\n%s", all)
	}
	for _, it := range g.items {
		if it.ID == e.ID && it.Fits {
			t.Fatal("the archived loop would be stopped")
		}
	}
	r, _ := clientResume(context.Background(), d.directory, e.ID, "prepare-resume", "", false, "", "", "", time.Now, nil)
	if len(r.Issues) == 0 || r.Issues[0] != "archived" || r.Ticket != "" {
		t.Fatalf("prepare-resume: %+v", r)
	}
	plain := readClientStatus(context.Background(), d.directory, e.ID, false, true, time.Minute, time.Now(), inspectDaemon)
	if plain.Snapshot == nil || plain.Snapshot.Archived == nil || plain.Snapshot.Archived.Note != "" || plain.Snapshot.Naming == nil || plain.Snapshot.Naming.Slug != e.Loop {
		t.Fatalf("client status: %+v", plain.Snapshot)
	}
	private := readClientStatus(context.Background(), d.directory, e.ID, true, true, time.Minute, time.Now(), inspectDaemon)
	if private.Snapshot == nil || private.Snapshot.Archived == nil || private.Snapshot.Archived.Note != "fictional note" {
		t.Fatalf("private client status: %+v", private.Snapshot)
	}
}

// A person's name is never trimmed, and a title that is only a route
// qualifier is kept as it is, never replaced by the slug.
func TestDisplayNameKeepsAPersonsNameAndAnAllRouteTitle(t *testing.T) {
	dir := t.TempDir()
	l := config.Loop{Name: "example-loop", StateDir: dir}
	if err := state.NoteThreadTitle(dir, "thr_example1", "(Claude 4)", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := namingOf(l, ""); n.NameSource != state.DisplayNameThreadTitle || n.DisplayName != "(Claude 4)" {
		t.Fatalf("all-route title: %+v", n)
	}
	if err := state.SaveDisplayName(dir, "Coordinator (Claude 4)", state.DisplayNameSet, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := namingOf(l, ""); n.DisplayName != "Coordinator (Claude 4)" || n.NameSource != state.DisplayNameSet {
		t.Fatalf("set name changed: %+v", n)
	}
}

// panel list text names each loop by its display name and, when that
// differs, its slug: two loops shown by one name stay apart.
func TestPanelListTextTellsTwoLoopsWithOneNameApart(t *testing.T) {
	a := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	a.Client.Snapshot.Naming = &loopNaming{DisplayName: "Coordinator", NameSource: state.DisplayNameSet, Slug: "example-loop"}
	b := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	b.Entry.ID, b.Entry.Loop = "loop_0123456789abcdef0123456789abcde2", "example-loop-b"
	b.Client.Snapshot.Naming = &loopNaming{DisplayName: "Coordinator", NameSource: state.DisplayNameSet, Slug: "example-loop-b"}
	c := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	c.Entry.ID, c.Entry.Loop = "loop_0123456789abcdef0123456789abcde3", "example-plain"
	list := buildPanelList(buildDockReport([]dockInput{a, b, c}, dockNow, 15*time.Second, 30*time.Minute, false, true, 0, true))
	var lines []string
	for _, l := range list.Loops {
		lines = append(lines, listNameLine(l.Name, l.Naming))
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"Coordinator (loop example-loop)", "Coordinator (loop example-loop-b)", "example-plain"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "(loop )") {
		t.Fatalf("empty slug: %q", got)
	}
}

// A BB thread that does not own the loop can neither archive nor unarchive
// it; the owner and a person outside BB can.
func TestArchiveIsTheOwnersOrAPersons(t *testing.T) {
	f := newFixture(t)
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	for _, c := range []string{"enable", "pause"} {
		if code, _, errs := f.run(c); code != exitOK {
			t.Fatalf("%s: %s", c, errs)
		}
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleother")
	if code, _, errs := f.run("archive"); code != exitFailed || !strings.Contains(errs, "archive refused: BB_THREAD_ID identifies a different thread from this loop's owner") {
		t.Fatalf("archive by another thread: %d %s", code, errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleowner")
	if code, _, errs := f.run("archive"); code != exitOK {
		t.Fatalf("archive by the owner: %s", errs)
	}
	t.Setenv("BB_THREAD_ID", "thr_exampleother")
	if code, _, errs := f.run("unarchive"); code != exitFailed || !strings.Contains(errs, "unarchive refused") {
		t.Fatalf("unarchive by another thread: %d %s", code, errs)
	}
	t.Setenv("BB_THREAD_ID", "")
	code, out, errs := f.run("unarchive")
	if code != exitOK || !strings.Contains(out, "Supervision is not running: its daemon exited when the loop was archived.") {
		t.Fatalf("unarchive by a person: %d %s %s", code, out, errs)
	}
}

// An archived loop waits on nobody: a paused or awaiting one carries no
// waiting record and no reason from it, in the dock or the list.
func TestArchivedLoopCarriesNoWaiting(t *testing.T) {
	at := dockNow.Add(-time.Hour)
	for _, c := range []*clientControls{{Enabled: true, Paused: true}, {Enabled: true, AwaitingUser: true}} {
		in := dockEvidence{controls: c}.input()
		in.Client.Snapshot.Archived = &archiveView{At: at, Hold: "paused"}
		r := buildDockReport([]dockInput{in}, dockNow, 15*time.Second, 30*time.Minute, false, true, 0, true)
		if l := r.Loops[0]; l.State != "archived" || l.Waiting != nil || len(r.NeedsYou) != 0 {
			t.Fatalf("%+v: dock %+v %+v", c, l.Waiting, r.NeedsYou)
		}
		row := buildPanelList(r).Loops[0]
		if row.Waiting != nil || row.NeedsYou || !strings.HasPrefix(row.Reason, "Archived since ") {
			t.Fatalf("%+v: list %+v %q", c, row.Waiting, row.Reason)
		}
	}
}

func TestArchiveNotesInstalledUserService(t *testing.T) {
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != exitOK {
		t.Fatal(errs)
	}
	if code, _, errs := f.run("pause"); code != exitOK {
		t.Fatal(errs)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.cfg), "state", serviceRecordName), []byte(`{"backend":"launchd"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := f.run("archive")
	if code != exitOK || !strings.Contains(out, "Its user service remains installed; remove it with `looptender service remove") {
		t.Fatalf("archive with service: code=%d out=%q err=%q", code, out, errs)
	}
}
