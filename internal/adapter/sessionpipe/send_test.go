package sessionpipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/config"
)

// The test binary stands in for the sessionpipe command: run with
// FAKE_SESSIONPIPE set, it acts as `sessionpipe ask` would in that situation
// and writes what it was given to FAKE_SESSIONPIPE_LOG. No real sessionpipe
// runs, and nothing reaches a real session.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_SESSIONPIPE"); mode != "" {
		os.Exit(fakeSessionpipe(mode))
	}
	os.Exit(m.Run())
}

func fakeSessionpipe(mode string) int {
	in, _ := io.ReadAll(os.Stdin)
	_, chained := os.LookupEnv("SESSIONPIPE_ASK_CHAIN")
	_, fromSession := os.LookupEnv("CLAUDE_CODE_SESSION_ID")
	_, testSwitch := os.LookupEnv("LOOPTENDER_TEST_FICTIONAL")
	_, token := os.LookupEnv("FICTIONAL_API_TOKEN")
	_ = os.WriteFile(os.Getenv("FAKE_SESSIONPIPE_LOG"), []byte(fmt.Sprintf("args=%s\nstdin=%s\nchained=%v\nfrom=%v\ntest=%v\ntoken=%v\nhome=%v\n",
		strings.Join(os.Args[1:], " "), in, chained, fromSession, testSwitch, token, os.Getenv("HOME") != "")), 0o600)
	switch {
	case mode == "deliver":
		// Accepted; the agent takes a while; then its answer, which must
		// never be read.
		time.Sleep(1500 * time.Millisecond)
		fmt.Println("FICTIONAL-ANSWER-TEXT")
		return 0
	case mode == "answer":
		fmt.Println("FICTIONAL-ANSWER-TEXT")
		return 0
	case strings.HasPrefix(mode, "refuse:"):
		fmt.Fprintf(os.Stderr, "sessionpipe ask: refused (%s): FICTIONAL daemon words about /a/folder\n", strings.TrimPrefix(mode, "refuse:"))
		return 1
	case mode == "nodaemon":
		fmt.Fprintln(os.Stderr, "sessionpipe ask: no control daemon is answering on this machine. The person runs `sessionpipe ask on` once at its terminal.")
		return 1
	case mode == "slow-nodaemon":
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "sessionpipe ask: no control daemon is answering on this machine. The person runs `sessionpipe ask on` once at its terminal.")
		return 1
	case mode == "chatty":
		// Far more stderr than is ever kept, then a refusal line at the end.
		fmt.Fprint(os.Stderr, strings.Repeat("FICTIONAL progress words\n", 20000))
		fmt.Fprintln(os.Stderr, "sessionpipe ask: refused (busy): late")
		return 1
	case mode == "fail":
		fmt.Fprintln(os.Stderr, "sessionpipe ask: claude-code:x failed: FICTIONAL failure words")
		return 1
	}
	return 2
}

type wakeFixture struct {
	*fixture
	log string
}

func newWakeFixture(t *testing.T, mode string) *wakeFixture {
	t.Helper()
	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "fake.log")
	// The stand-in's switches are given through extraEnv: the ask's own
	// environment is an allow-list, which drops them like any other name.
	t.Setenv("SESSIONPIPE_ASK_CHAIN", "claude-code:outer")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "fictional-supervisor-session")
	t.Setenv("LOOPTENDER_TEST_FICTIONAL", "1")
	t.Setenv("FICTIONAL_API_TOKEN", "not-a-real-token")
	f.a.wake, f.a.cli, f.a.ackWindow = true, os.Args[0], 300*time.Millisecond
	f.a.extraEnv = []string{"FAKE_SESSIONPIPE=" + mode, "FAKE_SESSIONPIPE_LOG=" + log}
	f.a.CheckAt(time.Now())
	f.a.now = time.Now
	return &wakeFixture{fixture: f, log: log}
}

// outcome waits for the wake's record to reach a final outcome: the stand-in
// is this test binary run again, which can take a while to start on a busy
// machine.
func (w *wakeFixture) outcome(t *testing.T) askRecord {
	t.Helper()
	var rec askRecord
	for i := 0; i < 200; i++ {
		if rec, _ = w.a.loadAsk(); rec.Outcome != "" && rec.Outcome != "delivering" {
			return rec
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the wake never finished: %+v", rec)
	return rec
}

func (w *wakeFixture) logged(t *testing.T) string {
	t.Helper()
	for i := 0; i < 500; i++ {
		if b, err := os.ReadFile(w.log); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the fake sessionpipe was not run")
	return ""
}

func TestWakeIsDeliveredThroughAskAndTheAnswerIsNeverRead(t *testing.T) {
	w := newWakeFixture(t, "deliver")
	start := time.Now()
	if err := w.a.Send(t.Context(), "Fictional wake text.", adapter.ModeAuto); err != nil {
		t.Fatalf("send: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Send waited %s: it must return after the acknowledgement window, not after the answer", d)
	}
	got := w.logged(t)
	for _, want := range []string{"args=ask " + harness + ":" + session + " --quiet --text -", "stdin=Fictional wake text.", "chained=false", "from=false", "test=false", "token=false", "home=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ask got %q; want %q", got, want)
		}
	}
	rec, ok := w.a.loadAsk()
	if !ok || rec.Outcome != "delivering" || rec.PID == 0 {
		t.Fatalf("record while delivering: %+v %v", rec, ok)
	}
	if since, pending := PendingWake(w.a.dir, time.Now()); !pending || !since.Equal(rec.StartedAt) {
		t.Fatalf("PendingWake while delivering: %v %v", since, pending)
	}
	// A record whose process ID now names another process is not pending.
	if rec.ProcStart != "" {
		other := rec
		other.ProcStart = "fictional:another-start"
		saved := rec
		if err := w.a.saveAsk(other); err != nil {
			t.Fatal(err)
		}
		if _, pending := PendingWake(w.a.dir, time.Now()); pending {
			t.Fatal("a reused process ID was taken for the pending wake")
		}
		if err := w.a.saveAsk(saved); err != nil {
			t.Fatal(err)
		}
	}
	// One wake at a time.
	if err := w.a.Send(t.Context(), "again", adapter.ModeAuto); !errors.As(err, new(adapter.ErrNotStarted)) {
		t.Fatalf("a second wake while the first is answered: %v", err)
	}
	// The detached ask finishes; its answer went nowhere.
	if rec := w.outcome(t); rec.Outcome != "answered" || rec.PID != 0 {
		t.Fatalf("record after the answer: %+v", rec)
	}
	if _, pending := PendingWake(w.a.dir, time.Now()); pending {
		t.Fatal("PendingWake after the answer")
	}
	for _, p := range []string{w.a.askPath(), w.a.statePath()} {
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "FICTIONAL") {
			t.Errorf("%s keeps text from sessionpipe: %s", filepath.Base(p), b)
		}
	}
}

func TestWakeRefusalsSendNothingAndKeepOnlyTheCode(t *testing.T) {
	for mode, want := range map[string]string{
		"refuse:ask_off":   "ask_off",
		"refuse:folder":    "folder",
		"refuse:weird_new": "other",
		"nodaemon":         "no_daemon",
	} {
		t.Run(mode, func(t *testing.T) {
			w := newWakeFixture(t, mode)
			// A refusal is told within the window; give a slow start room.
			w.a.ackWindow = 10 * time.Second
			err := w.a.Send(t.Context(), "Fictional wake text.", adapter.ModeAuto)
			var ns adapter.ErrNotStarted
			if !errors.As(err, &ns) || !strings.Contains(ns.Reason, "("+want+")") || strings.Contains(err.Error(), "FICTIONAL") {
				t.Fatalf("got %v, want a not-started refusal naming %s and no daemon words", err, want)
			}
			rec, _ := w.a.loadAsk()
			if rec.Outcome != "refused" || rec.Code != want {
				t.Fatalf("record %+v", rec)
			}
			if b, _ := os.ReadFile(w.a.askPath()); strings.Contains(string(b), "FICTIONAL") {
				t.Fatalf("the ask's stderr was kept: %s", b)
			}
		})
	}
	// Stderr past the cap is read and discarded: a refusal line after it is
	// not seen, so the ask counts as failed, not as refused.
	w0 := newWakeFixture(t, "chatty")
	w0.a.ackWindow = 10 * time.Second
	if err := w0.a.Send(t.Context(), "Fictional wake text.", adapter.ModeAuto); err == nil || errors.As(err, new(adapter.ErrNotStarted)) {
		t.Fatalf("a chatty ask: %v", err)
	}
	if entries, _ := os.ReadDir(w0.a.dir); len(entries) > 2 {
		t.Fatalf("files beside the records: %v", entries)
	}
	// A failure without a refusal code may have reached the session.
	w := newWakeFixture(t, "fail")
	w.a.ackWindow = 10 * time.Second
	err := w.a.Send(t.Context(), "Fictional wake text.", adapter.ModeAuto)
	if err == nil || errors.As(err, new(adapter.ErrNotStarted)) || strings.Contains(err.Error(), "FICTIONAL") {
		t.Fatalf("a failure: %v", err)
	}
	// An answer within the window is a delivered wake.
	w = newWakeFixture(t, "answer")
	if err := w.a.Send(t.Context(), "Fictional wake text.", adapter.ModeAuto); err != nil {
		t.Fatalf("a quick answer: %v", err)
	}
	if rec := w.outcome(t); rec.Outcome != "answered" {
		t.Fatalf("record %+v", rec)
	}
}

func TestWakeOnlyWhenOptedInAndNeverQueued(t *testing.T) {
	f := newFixture(t)
	if err := f.a.Send(t.Context(), "x", adapter.ModeAuto); !errors.As(err, new(adapter.ErrUnsupported)) {
		t.Fatalf("without send: wake_only: %v", err)
	}
	opted := New(config.Sessionpipe{Session: harness + ":" + session, EventsFile: f.file, Send: config.SessionpipeSendWakeOnly, CLI: "absent-sessionpipe"}, t.TempDir())
	if !opted.wake {
		t.Fatal("send: wake_only did not opt in")
	}
	if err := opted.Send(t.Context(), "x", adapter.ModeQueue); !errors.As(err, new(adapter.ErrUnsupported)) {
		t.Fatalf("a queued message (a nudge): %v", err)
	}
	if err := opted.Send(t.Context(), "  ", adapter.ModeAuto); !errors.As(err, new(adapter.ErrNotStarted)) {
		t.Fatalf("an empty wake: %v", err)
	}
	if err := opted.Send(t.Context(), "x", adapter.ModeAuto); !errors.As(err, new(adapter.ErrNotStarted)) {
		t.Fatalf("no sessionpipe command: %v", err)
	}
	if err := opted.Stop(t.Context()); !errors.As(err, new(adapter.ErrUnsupported)) {
		t.Fatalf("stop: %v", err)
	}
	if got := strings.Join(askEnv([]string{"HOME=/h", "PATH=/p", "CLAUDE_CODE_SESSION_ID=s", "CODEX_SESSION_ID=c", "SESSIONPIPE_ASK_CHAIN=a", "SESSIONPIPE_SOCKET=/s", "LOOPTENDER_TEST_X=1", "KEEPGOING_TEST_Y=1", "ANTHROPIC_API_KEY=k", "GITHUB_TOKEN=t"}), ","); got != "HOME=/h,PATH=/p,SESSIONPIPE_SOCKET=/s" {
		t.Fatalf("allow-listed env: %s", got)
	}
}

// A start time that cannot be read at check time, while the process is
// alive, leaves the wake pending; another start time means another process.
func TestPendingWakeWhenTheStartTimeCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	a := &Adapter{dir: dir}
	if err := a.saveAsk(askRecord{PID: os.Getpid(), ProcStart: "recorded-start", StartedAt: time.Now(), Outcome: "delivering"}); err != nil {
		t.Fatal(err)
	}
	defer func(f func(int) string) { readProcStart = f }(readProcStart)
	readProcStart = func(int) string { return "" }
	if _, pending := PendingWake(dir, time.Now()); !pending {
		t.Fatal("an unreadable start time reads as finished")
	}
	readProcStart = func(int) string { return "another-start" }
	if _, pending := PendingWake(dir, time.Now()); pending {
		t.Fatal("another start time reads as the same process")
	}
	readProcStart = func(int) string { return "recorded-start" }
	if _, pending := PendingWake(dir, time.Now()); !pending {
		t.Fatal("the same start time reads as finished")
	}
	readProcStart = func(int) string { return "" }
	if _, pending := PendingWake(dir, time.Now().Add(25*time.Hour)); pending {
		t.Fatal("an unreadable start time holds past the day bound")
	}
}

// A wake that did not get through holds the next until the session's next
// turn event or the interval, whichever comes first; an answered one never
// holds.
func TestWakeHoldAfterAWakeThatDidNotGetThrough(t *testing.T) {
	dir := t.TempDir()
	a := &Adapter{dir: dir}
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for _, outcome := range []string{"refused", "failed", "unknown"} {
		if err := a.saveAsk(askRecord{StartedAt: at, Outcome: outcome, Code: "folder"}); err != nil {
			t.Fatal(err)
		}
		held, got, code, until := WakeHold(dir, at.Add(10*time.Minute), time.Hour)
		if !held || got != outcome || code != "folder" || !until.Equal(at.Add(time.Hour)) {
			t.Fatalf("%s: held=%v %s %s %s", outcome, held, got, code, until)
		}
		if held, _, _, _ := WakeHold(dir, at.Add(time.Hour), time.Hour); held {
			t.Fatalf("%s: held after the interval", outcome)
		}
	}
	// The session's next turn ends the hold before the interval.
	if err := a.save(readState{Version: readStateVersion, LastTurnAt: at.Add(5 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if held, _, _, _ := WakeHold(dir, at.Add(10*time.Minute), time.Hour); held {
		t.Fatal("held after the session's next turn")
	}
	// An answered wake, or none at all, holds nothing.
	if err := a.saveAsk(askRecord{StartedAt: at, Outcome: "answered"}); err != nil {
		t.Fatal(err)
	}
	if held, _, _, _ := WakeHold(dir, at.Add(time.Minute), time.Hour); held {
		t.Fatal("an answered wake holds the next")
	}
	if held, _, _, _ := WakeHold(t.TempDir(), at, time.Hour); held {
		t.Fatal("no record holds")
	}
}

// The reader keeps the time of the session's last turn event.
func TestObserveKeepsTheLastTurnTime(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil), line("turn.started", 2, t0.Add(time.Minute), nil), line("session.heartbeat", 3, t0.Add(2*time.Minute), nil))
	f.observe(t0.Add(2 * time.Minute))
	if s := f.a.load(); !s.LastTurnAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("last turn %s", s.LastTurnAt)
	}
}

// A wake recorded after now (the clock was set back) holds nothing.
func TestWakeHoldIgnoresAWakeRecordedInTheFuture(t *testing.T) {
	dir := t.TempDir()
	a := &Adapter{dir: dir}
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if err := a.saveAsk(askRecord{StartedAt: now.Add(3 * time.Hour), Outcome: "refused", Code: "folder"}); err != nil {
		t.Fatal(err)
	}
	if held, _, _, _ := WakeHold(dir, now, time.Hour); held {
		t.Fatal("a wake recorded in the future holds the next")
	}
}

// Cancelling the check context mid-wake still records the refusal once the
// ask command exits, so the next check holds back rather than retrying blindly.
func TestSendContextCancelledStillRecordsOutcome(t *testing.T) {
	w := newWakeFixture(t, "slow-nodaemon")
	w.a.ackWindow = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := w.a.Send(ctx, "Carry on", adapter.ModeAuto); err != nil {
		t.Fatalf("cancelled Send returned error: %v", err)
	}
	rec := w.outcome(t)
	if rec.Outcome != "refused" || rec.Code != "no_daemon" {
		t.Fatalf("after ctx cancel: %+v, want refused/no_daemon", rec)
	}
	if held, _, code, _ := WakeHold(w.a.dir, rec.StartedAt.Add(5*time.Minute), time.Hour); !held || code != "no_daemon" {
		t.Fatalf("WakeHold after late refusal: held=%v code=%q", held, code)
	}
}

// PendingWake ignores future-dated records (clock jump backward) as well as
// corrupt or oversized records.
func TestPendingWakeIgnoresFutureClockAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	a := &Adapter{dir: dir}
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if err := a.saveAsk(askRecord{PID: os.Getpid(), StartedAt: now.Add(time.Hour), Outcome: "delivering"}); err != nil {
		t.Fatal(err)
	}
	if _, pending := PendingWake(dir, now); pending {
		t.Fatal("future-dated wake treated as pending")
	}
	if err := os.WriteFile(a.askPath(), []byte(`{"pad":"`+strings.Repeat("x", maxAskRecord+64)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, pending := PendingWake(dir, now); pending {
		t.Fatal("oversized record treated as pending")
	}
}

// A late background completion from an earlier wake never overwrites a newer
// wake already recorded in sessionpipe-ask.json.
func TestLateEndedDoesNotOverwriteNewerWake(t *testing.T) {
	dir := t.TempDir()
	a := &Adapter{dir: dir}
	tOld := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	tNew := tOld.Add(2 * time.Minute)
	newer := askRecord{PID: os.Getpid(), StartedAt: tNew, Outcome: "delivering"}
	if err := a.saveAsk(newer); err != nil {
		t.Fatal(err)
	}
	older := askRecord{PID: os.Getpid() + 999999, StartedAt: tOld, Outcome: "delivering"}
	_ = a.ended(older, errors.New("exit status 1"), []byte("sessionpipe ask: no control daemon is answering\n"))
	got, ok := a.loadAsk()
	if !ok || got.Outcome != "delivering" || !got.StartedAt.Equal(tNew) || got.PID != newer.PID {
		t.Fatalf("newer wake overwritten: %+v (ok=%v)", got, ok)
	}
}
