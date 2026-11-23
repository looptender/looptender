package sessionpipe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
)

// The session under observation and another one in the same file. The
// identifiers are fictional.
const (
	harness = "claude-code"
	session = "8c132906-8c3f-4814-870a-afc3d05e1d2e"
	other   = "01a10860-a43f-7ce2-b12f-a7911ae8a880"
	// secret stands for every word a line may carry that looptender must
	// never keep: a title cut from a prompt, a folder, a message.
	secret = "FICTIONAL-PRIVATE-WORDS"
)

var t0 = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

// line is one event as sessionpipe's tier-0 file sink writes it, with the
// session block a real sink carries.
func line(typ string, seq int, at time.Time, data map[string]any) string {
	return lineFor(session, 0, typ, seq, at, data)
}

func lineFor(id string, tier int, typ string, seq int, at time.Time, data map[string]any) string {
	if data == nil {
		data = map[string]any{}
	}
	e := map[string]any{
		"protocol": 1, "id": "01JD0000000000000000000000", "type": typ, "time": at.Format("2006-01-02T15:04:05.000Z"), "tier": tier,
		"harness": map[string]any{"name": harness, "version": "2.1.0"},
		"session": map[string]any{"id": id, "seq": seq, "machine": "example-machine", "cwd": "~/" + secret, "branch": secret, "model": "example-model",
			"title": secret, "title_source": "first-ask", "account_id": "a1b2"},
		"privacy": map[string]any{"rulesets": []string{}, "pii": false},
		"data":    data,
	}
	b, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

type fixture struct {
	t    *testing.T
	file string
	a    *Adapter
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(config.Sessionpipe{Session: harness + ":" + session, EventsFile: file}, filepath.Join(dir, "state", "targets", "primary"))
	a.CheckAt(t0)
	return &fixture{t: t, file: file, a: a}
}

func (f *fixture) append(lines ...string) {
	f.t.Helper()
	w, err := os.OpenFile(f.file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.WriteString(strings.Join(lines, "")); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) observe(at time.Time) decide.Observation {
	f.t.Helper()
	f.a.CheckAt(at)
	o, err := f.a.Observe(f.t.Context())
	if err != nil {
		f.t.Fatalf("observe: %v", err)
	}
	return o
}

func TestObserveTierZeroLifecycle(t *testing.T) {
	f := newFixture(t)
	if o := f.observe(t0); o.State != decide.Unknown {
		t.Fatalf("an empty file: %s, want unknown", o.State)
	}
	f.append(line("session.started", 1, t0, map[string]any{"source": "startup"}))
	if o := f.observe(t0.Add(time.Second)); o.State != decide.Idle {
		t.Fatalf("after session.started: %s, want idle", o.State)
	}
	f.append(line("turn.started", 2, t0.Add(2*time.Second), map[string]any{"turn_id": "turn-1", "submitted_by": "user"}),
		lineFor(other, 0, "turn.ended", 9, t0.Add(3*time.Second), map[string]any{"reason": "stop"}))
	if o := f.observe(t0.Add(4 * time.Second)); o.State != decide.Active {
		t.Fatalf("after turn.started (another session ended): %s, want active", o.State)
	}
	f.append(line("session.heartbeat", 3, t0.Add(65*time.Second), nil))
	if o := f.observe(t0.Add(9 * time.Minute)); o.State != decide.Active {
		t.Fatalf("a heartbeat keeps a running turn fresh: %s", o.State)
	}
	f.append(line("turn.ended", 4, t0.Add(10*time.Minute), map[string]any{"turn_id": "turn-1", "reason": "stop", "background": 2}))
	o := f.observe(t0.Add(10 * time.Minute))
	if o.State != decide.Idle || o.Background != 2 {
		t.Fatalf("after turn.ended with background work: %s background %d, want idle 2", o.State, o.Background)
	}
	f.append(line("turn.started", 5, t0.Add(11*time.Minute), nil), line("turn.ended", 6, t0.Add(12*time.Minute), map[string]any{"reason": "interrupt"}))
	if o := f.observe(t0.Add(12 * time.Minute)); o.State != decide.Idle || o.Background != 0 {
		t.Fatalf("an interrupted turn: %s background %d, want idle 0", o.State, o.Background)
	}
	// A line written in part is left for the next check.
	half := line("turn.started", 7, t0.Add(13*time.Minute), nil)
	f.append(half[:20])
	if o := f.observe(t0.Add(13 * time.Minute)); o.State != decide.Idle {
		t.Fatalf("a partial line was used: %s", o.State)
	}
	f.append(half[20:])
	if o := f.observe(t0.Add(13 * time.Minute)); o.State != decide.Active {
		t.Fatalf("the completed line was not used: %s", o.State)
	}
	if key, known := f.a.ConversationKey(); !known || key != harness+":"+session {
		t.Fatalf("conversation key %q %v", key, known)
	}
}

func TestObserveSilenceIsUnknown(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil), line("turn.started", 2, t0, nil))
	if o := f.observe(t0.Add(config.DefaultSessionpipeStaleAfter + time.Second)); o.State != decide.Unknown {
		t.Fatalf("a running turn silent past stale_after: %s, want unknown", o.State)
	}
	f.append(line("turn.ended", 3, t0.Add(20*time.Minute), map[string]any{"reason": "stop"}))
	if o := f.observe(t0.Add(21 * time.Minute)); o.State != decide.Idle {
		t.Fatalf("a fresh turn end: %s, want idle", o.State)
	}
	if o := f.observe(t0.Add(20*time.Minute + config.DefaultSessionpipeStaleAfter + time.Second)); o.State != decide.Unknown {
		t.Fatalf("an idle session silent past stale_after: %s, want unknown, never idle", o.State)
	}
	// A clock far ahead does not keep a session fresh for ever.
	f.append(line("turn.started", 4, t0.Add(1000*time.Hour), nil))
	at := t0.Add(40 * time.Minute)
	if o := f.observe(at); o.State != decide.Active {
		t.Fatalf("an event from the future: %s", o.State)
	}
	if o := f.observe(at.Add(config.DefaultSessionpipeStaleAfter + time.Second)); o.State != decide.Unknown {
		t.Fatalf("an event from the future kept the session fresh: %s", o.State)
	}
}

func TestObserveWaitingOnPerson(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil), line("turn.started", 2, t0, nil),
		line("attention.needed", 3, t0.Add(time.Second), map[string]any{"attention_id": "att-1", "kind": "permission", "message": secret, "tool": "Bash"}))
	o := f.observe(t0.Add(2 * time.Second))
	if !o.AwaitingInput {
		t.Fatalf("a permission prompt does not read as waiting on a person: %+v", o)
	}
	f.append(line("attention.cleared", 4, t0.Add(3*time.Second), map[string]any{"attention_id": "att-1", "how": "answered"}))
	if o := f.observe(t0.Add(4 * time.Second)); o.AwaitingInput || o.State != decide.Active {
		t.Fatalf("after attention.cleared: %+v", o)
	}
	// No sender may clear it: the turn's end does.
	f.append(line("attention.needed", 5, t0.Add(5*time.Second), map[string]any{"attention_id": "att-2", "kind": "question"}))
	if o := f.observe(t0.Add(6 * time.Second)); !o.AwaitingInput {
		t.Fatal("a question does not read as waiting on a person")
	}
	f.append(line("turn.ended", 6, t0.Add(7*time.Second), map[string]any{"reason": "stop"}))
	if o := f.observe(t0.Add(8 * time.Second)); o.AwaitingInput || o.State != decide.Idle {
		t.Fatalf("a turn's end did not close the attention: %+v", o)
	}
	// An idle notification is not a question.
	f.append(line("attention.needed", 7, t0.Add(9*time.Second), map[string]any{"attention_id": "att-3", "kind": "idle"}))
	if o := f.observe(t0.Add(10 * time.Second)); o.AwaitingInput || o.State != decide.Idle {
		t.Fatalf("an idle notification: %+v", o)
	}
}

func TestErrorTurnHasNoClass(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil), line("turn.started", 2, t0, map[string]any{"turn_id": "turn-9"}),
		line("turn.ended", 3, t0.Add(time.Second), map[string]any{"turn_id": "turn-9", "reason": "error", "error": "rate_limit"}))
	o := f.observe(t0.Add(2 * time.Second))
	if o.State != decide.Error || o.ErrorClass != decide.ErrUnknown || o.ErrorTurn != "turn-9" {
		t.Fatalf("an errored turn: %+v, want error of class unknown on turn-9", o)
	}
	if o.Evidence.Source != EvidenceSource || o.Evidence.Code != "unavailable" {
		t.Fatalf("evidence %+v", o.Evidence)
	}
}

func TestObserveEndedIsGone(t *testing.T) {
	for _, typ := range []string{"session.ended", "session.forgotten"} {
		f := newFixture(t)
		f.append(line("session.started", 1, t0, nil), line(typ, 2, t0.Add(time.Second), map[string]any{"reason": "exit"}))
		// Gone holds whatever the time: an ended session does not go stale.
		if o := f.observe(t0.Add(48 * time.Hour)); !o.Gone {
			t.Fatalf("%s: %+v, want gone", typ, o)
		}
	}
}

func TestObserveRefusesAboveTierZero(t *testing.T) {
	for name, bad := range map[string]string{
		"tier 1":         lineFor(other, 1, "tool.ended", 1, t0, map[string]any{"tool": "Bash"}),
		"tier 2":         lineFor(session, 2, "turn.transcript", 1, t0, map[string]any{"user": secret}),
		"no tier":        strings.Replace(line("turn.started", 1, t0, nil), `"tier":0,`, "", 1),
		"other protocol": strings.Replace(line("turn.started", 1, t0, nil), `"protocol":1`, `"protocol":2`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.append(line("session.started", 1, t0, nil), bad, line("turn.started", 2, t0, nil))
			f.a.CheckAt(t0)
			o, err := f.a.Observe(t.Context())
			if !errors.Is(err, ErrNotTierZero) || o.State != decide.Unknown {
				t.Fatalf("got %s, %v; want unknown and ErrNotTierZero", o.State, err)
			}
			// It stays refused until the file is replaced.
			if _, err := f.a.Observe(t.Context()); !errors.Is(err, ErrNotTierZero) {
				t.Fatalf("the second read: %v", err)
			}
			if err := os.WriteFile(f.file, []byte(line("session.started", 1, t0, nil)), 0o600); err != nil {
				t.Fatal(err)
			}
			if o := f.observe(t0); o.State != decide.Idle {
				t.Fatalf("a replaced tier-0 file: %s", o.State)
			}
		})
	}
}

func TestObserveKeepsNoText(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil),
		line("turn.started", 2, t0, map[string]any{"turn_id": "a turn id with " + secret}),
		line("attention.needed", 3, t0, map[string]any{"attention_id": secret + " words", "kind": "question"}),
		line("example.extension", 4, t0, map[string]any{"reason": secret, "note": secret}),
		line("turn.ended", 5, t0, map[string]any{"reason": secret}))
	o := f.observe(t0)
	if o.State != decide.Active || o.AwaitingInput || o.ErrorTurn != "" {
		t.Fatalf("free text was taken for a protocol word: %+v", o)
	}
	b, err := os.ReadFile(f.a.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "example-machine") || strings.Contains(string(b), "example-model") {
		t.Fatalf("the reader's record keeps text from the file: %s", b)
	}
	if info, err := os.Stat(f.a.statePath()); err != nil || (isUnix() && info.Mode().Perm() != 0o600) {
		t.Fatalf("the reader's record is not private: %v %v", info.Mode(), err)
	}
	// An error never names the file's path.
	missing := New(config.Sessionpipe{Session: harness + ":" + session, EventsFile: filepath.Join(t.TempDir(), secret)}, t.TempDir())
	if _, err := missing.Observe(t.Context()); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("a missing file: %v", err)
	}
}

func TestObserveReplacedFileIsReadAgain(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil), line("turn.started", 2, t0, nil), line("turn.ended", 3, t0, map[string]any{"reason": "stop"}))
	if o := f.observe(t0); o.State != decide.Idle {
		t.Fatalf("%s", o.State)
	}
	// Rotated: a new, shorter file whose sequence starts again.
	if err := os.WriteFile(f.file, []byte(line("turn.started", 1, t0.Add(time.Minute), nil)), 0o600); err != nil {
		t.Fatal(err)
	}
	if o := f.observe(t0.Add(time.Minute)); o.State != decide.Active {
		t.Fatalf("a replaced file was not read from its start: %s", o.State)
	}
	// An event already applied is not applied again.
	f.append(line("turn.ended", 1, t0.Add(2*time.Minute), map[string]any{"reason": "stop"}))
	if o := f.observe(t0.Add(2 * time.Minute)); o.State != decide.Active {
		t.Fatalf("a repeated sequence number was applied: %s", o.State)
	}
}

func TestObserveBoundedRead(t *testing.T) {
	f := newFixture(t)
	// A line far over MaxLine is skipped, not used.
	long := strings.Replace(line("turn.started", 1, t0, map[string]any{"pad": strings.Repeat("x", MaxLine)}), "\n", "", 1)
	var b strings.Builder
	b.WriteString(long + "\n")
	b.WriteString(line("session.started", 2, t0, nil))
	// A backlog over MaxReadPerCheck is read over several checks, unknown
	// until it has been read to the end.
	filler := lineFor(other, 0, "session.heartbeat", 1, t0, nil)
	for b.Len() < MaxReadPerCheck+len(filler) {
		b.WriteString(filler)
	}
	b.WriteString(line("turn.started", 3, t0, nil))
	f.append(b.String())
	if o := f.observe(t0); o.State != decide.Unknown {
		t.Fatalf("a backlog not yet read: %s, want unknown", o.State)
	}
	o := f.observe(t0)
	for i := 0; i < 4 && o.State == decide.Unknown; i++ {
		o = f.observe(t0)
	}
	if o.State != decide.Active {
		t.Fatalf("after the backlog: %s, want active", o.State)
	}
	s := f.a.load()
	if s.Skipped != 1 {
		t.Fatalf("skipped %d lines, want the one long line", s.Skipped)
	}
}

func TestSendAndStopAreUnsupported(t *testing.T) {
	f := newFixture(t)
	if err := f.a.Send(t.Context(), "anything", adapter.ModeAuto); !errors.As(err, new(adapter.ErrUnsupported)) {
		t.Fatalf("send: %v", err)
	}
	if err := f.a.Stop(t.Context()); !errors.As(err, new(adapter.ErrUnsupported)) {
		t.Fatalf("stop: %v", err)
	}
	if b, _ := os.ReadFile(f.file); len(b) != 0 {
		t.Fatal("the events file was written")
	}
	if f.a.Name() != config.SessionpipeAdapter {
		t.Fatal(f.a.Name())
	}
}

func isUnix() bool { return runtime.GOOS != "windows" }
