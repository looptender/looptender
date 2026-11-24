//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/telegram"
)

// tgStandin is an offline Bot API stand-in: updates are queued, and every
// sendMessage and answerCallbackQuery is kept. It never reaches Telegram.
type tgStandin struct {
	t       *testing.T
	tok     string
	srv     *httptest.Server
	mu      sync.Mutex
	queued  []telegram.Update
	sent    []tgSentRecord
	answers []tgCallbackAnswerRecord
	nextID  int64
	tgDir   string
	regDir  string
}

func newTGStandin(t *testing.T, tok, tgDir, regDir string) *tgStandin {
	s := &tgStandin{t: t, tok: tok, tgDir: tgDir, regDir: regDir, nextID: 100}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+tok+"/")
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		defer s.mu.Unlock()
		switch method {
		case "getUpdates":
			b, _ := json.Marshal(map[string]any{"ok": true, "result": s.queued})
			s.queued = nil
			_, _ = w.Write(b)
		case "sendMessage":
			var m tgSentRecord
			_ = json.NewDecoder(r.Body).Decode(&m)
			s.sent = append(s.sent, m)
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":200,"date":1795000200}}`))
		case "answerCallbackQuery":
			var a tgCallbackAnswerRecord
			_ = json.NewDecoder(r.Body).Decode(&a)
			s.answers = append(s.answers, a)
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// send queues one update from the paired user and runs one poll cycle,
// returning what the bot sent in reply.
func (s *tgStandin) send(u telegram.Update) []tgSentRecord {
	s.t.Helper()
	s.mu.Lock()
	s.nextID++
	u.UpdateID = s.nextID
	s.queued = []telegram.Update{u}
	before := len(s.sent)
	s.mu.Unlock()
	var out, errb bytes.Buffer
	if code := telegramCommand(env{ctx: context.Background(), stdout: &out, stderr: &errb}, []string{"poll", "--once", "--offline", "--dir", s.tgDir, "--registry", s.regDir, "--api-url", s.srv.URL, "--poll-wait", "1s"}); code != exitOK {
		s.t.Fatalf("poll: %d %s", code, errb.String())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tgSentRecord{}, s.sent[before:]...)
}

func tgCommand(text string) telegram.Update {
	return telegram.Update{Message: &telegram.Message{MessageID: 1, Date: time.Now().Unix(), From: &telegram.User{ID: 7001}, Chat: telegram.Chat{ID: 7001, Type: "private"}, Text: text}}
}

func tgTap(data string) telegram.Update {
	return telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "cb", From: telegram.User{ID: 7001}, Message: &telegram.Message{MessageID: 2, Date: time.Now().Unix(), Chat: telegram.Chat{ID: 7001, Type: "private"}}, Data: data}}
}

func tgButton(t *testing.T, msgs []tgSentRecord, prefix string) string {
	t.Helper()
	for _, m := range msgs {
		if m.ReplyMarkup == nil {
			continue
		}
		for _, row := range m.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				if strings.HasPrefix(b.Text, prefix) {
					return b.CallbackData
				}
			}
		}
	}
	return ""
}

// A stopped loop is not started again by the tap that answers a question:
// the review only asks, the second message asks in its own words, and only
// its button clears the holds.
func TestTelegramStopNeedsItsOwnConfirmation(t *testing.T) {
	const tok = "123456789:EXAMPLE_FIRST_USE_TOKEN"
	tgDir := setupPairedTelegramDir(t, tok, time.Now().UTC())
	regDir, entry := clientFixture(t)
	if err := control.Save(entry.StateDir, control.File{Enabled: true, Stop: true, AwaitingUser: true, Note: "PRIVATE-NOTE"}); err != nil {
		t.Fatal(err)
	}
	s := newTGStandin(t, tok, tgDir, regDir)
	prep := tgButton(t, s.send(tgCommand("/status")), "Prepare resume ")
	review := s.send(tgTap(prep))
	again := tgButton(t, review, "Start ")
	if again == "" || strings.Contains(review[0].Text, "PRIVATE-NOTE") || !strings.Contains(review[0].Text, "Nothing is cleared yet") {
		t.Fatalf("review: %+v", review)
	}
	if cb, ok := telegram.DecodeControlCallback(again); !ok || cb.Action != telegram.ActionRestart {
		t.Fatalf("review button: %+v", cb)
	}
	question := s.send(tgTap(again))
	confirm := tgButton(t, question, "Yes, start ")
	if confirm == "" || !strings.Contains(question[0].Text, "This loop was stopped. Start it again?") {
		t.Fatalf("question: %+v", question)
	}
	if c, _ := control.Load(entry.StateDir); !c.Stop || !c.AwaitingUser {
		t.Fatalf("cleared before the second confirmation: %+v", c)
	}
	s.send(tgTap(confirm))
	if c, _ := control.Load(entry.StateDir); c.Stop || c.AwaitingUser {
		t.Fatalf("not cleared after it: %+v", c)
	}
}

// Prepare resume is not offered where a resume would leave the loop off.
func TestTelegramPrepareResumeNotOfferedForOffLoops(t *testing.T) {
	const tok = "123456789:EXAMPLE_FIRST_USE_TOKEN"
	for name, setup := range map[string]func(dir string){
		"superseded": func(dir string) {
			_ = control.Save(dir, control.File{Enabled: true, Paused: true, SupersededBy: "thr_example000"})
		},
		"disabled": func(dir string) { _ = control.Save(dir, control.File{Paused: true}) },
		"archived": func(dir string) {
			_ = control.Save(dir, control.File{Enabled: true, Paused: true})
			_, _, _ = control.ArchiveLoop(dir, "", time.Now(), nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			tgDir := setupPairedTelegramDir(t, tok, time.Now().UTC())
			regDir, entry := clientFixture(t)
			setup(entry.StateDir)
			s := newTGStandin(t, tok, tgDir, regDir)
			if got := tgButton(t, s.send(tgCommand("/status")), "Prepare resume "); got != "" {
				t.Fatalf("Prepare resume offered for a %s loop", name)
			}
		})
	}
}

// A token typed at a terminal would be shown: setup refuses and gives the
// safe command, before any request.
func TestTelegramSetupNeverReadsATypedToken(t *testing.T) {
	t.Setenv(telegram.TokenEnv, "")
	tty, err := os.Open("/dev/null") // a character device, as a terminal is
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	for _, args := range [][]string{{"setup", "--dir", t.TempDir()}, {"setup", "--token-stdin", "--dir", t.TempDir()}} {
		var out, errb bytes.Buffer
		code := telegramCommandWithInput(env{ctx: context.Background(), stdout: &out, stderr: &errb}, args, tty)
		if code != exitUsage || !strings.Contains(errb.String(), "refusing to read the bot token from the terminal") || !strings.Contains(errb.String(), "read -rs") {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), errb.String())
		}
	}
}

// The poller service: what is written and run on each platform, never from
// a BB thread, and gone again on uninstall.
func TestTelegramServiceInstallStatusUninstall(t *testing.T) {
	const tok = "123456789:EXAMPLE_FIRST_USE_TOKEN"
	tgDir := setupPairedTelegramDir(t, tok, time.Now().UTC())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")
	var ran [][]string
	oldRun, oldGOOS, oldUID := telegramServiceRun, telegramServiceGOOS, telegramServiceUID
	t.Cleanup(func() { telegramServiceRun, telegramServiceGOOS, telegramServiceUID = oldRun, oldGOOS, oldUID })
	telegramServiceRun = func(argv []string) ([]byte, error) { ran = append(ran, argv); return nil, nil }
	telegramServiceUID = func() int { return 501 }
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := telegramCommand(env{ctx: context.Background(), stdout: &out, stderr: &errb}, append([]string{"service"}, append(args, "--dir", tgDir)...))
		return code, out.String(), errb.String()
	}
	for _, goos := range []string{"darwin", "linux"} {
		telegramServiceGOOS, ran = goos, nil
		code, out, _ := run("install", "--dry-run")
		if code != exitOK || len(ran) != 0 || !strings.Contains(out, "telegram</string><string>poll") && !strings.Contains(out, "ExecStart=") {
			t.Fatalf("%s dry run: %d %s %v", goos, code, out, ran)
		}
		if goos == "darwin" && (!strings.Contains(out, "<key>LimitLoadToSessionType</key><string>Background</string>") || !strings.Contains(out, "launchctl bootstrap user/501 ")) {
			t.Fatalf("darwin plan: %s", out)
		}
		if goos == "linux" && (!strings.Contains(out, "systemctl --user enable --now looptender-telegram-poll.service") || !strings.Contains(out, "UnsetEnvironment=LOOPTENDER_TELEGRAM_BOT_TOKEN LOOPTENDER_TELEGRAM_API_URL")) {
			t.Fatalf("linux plan: %s", out)
		}
		t.Setenv("BB_THREAD_ID", "thr_exampleagent")
		if code, _, errs := run("install"); code != exitFailed || !strings.Contains(errs, "not from a BB thread") {
			t.Fatalf("%s from a thread: %d %s", goos, code, errs)
		}
		t.Setenv("BB_THREAD_ID", "")
		if code, out, errs := run("install"); code != exitOK || !strings.Contains(out, "Installed the Telegram poller") {
			t.Fatalf("%s install: %d %s %s", goos, code, out, errs)
		}
		file := map[string]string{"darwin": filepath.Join(home, "Library", "LaunchAgents", telegramServiceLabel+".plist"), "linux": filepath.Join(home, ".config", "systemd", "user", telegramServiceUnit)}[goos]
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("%s: no service file: %v", goos, err)
		}
		if _, out, _ := run("status"); !strings.Contains(out, "Service: installed") || !strings.Contains(out, "Poller: not running") {
			t.Fatalf("%s status: %s", goos, out)
		}
		if code, _, _ := run("uninstall"); code != exitOK {
			t.Fatal("uninstall")
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("%s: service file kept", goos)
		}
	}
}

func TestTelegramPollRefusesThreadOrSupervisedEnv(t *testing.T) {
	for _, kv := range [][2]string{{"BB_THREAD_ID", "thr_example"}, {control.SupervisedLoopVar, "example-loop"}} {
		t.Setenv(kv[0], kv[1])
		var out, errb bytes.Buffer
		code := telegramCommand(env{ctx: context.Background(), stdout: &out, stderr: &errb}, []string{"poll", "--once"})
		if code != exitFailed || !strings.Contains(errb.String(), "refused") {
			t.Fatalf("%s=%s: code=%d err=%s", kv[0], kv[1], code, errb.String())
		}
		t.Setenv(kv[0], "")
	}
}

func TestTelegramStopRequiresRestartConfirmationInHandler(t *testing.T) {
	const tok = "123456789:EXAMPLE_FIRST_USE_TOKEN"
	tgDir := setupPairedTelegramDir(t, tok, time.Now().UTC())
	regDir, entry := clientFixture(t)
	if err := control.Save(entry.StateDir, control.File{Enabled: true, Stop: true, AwaitingUser: true}); err != nil {
		t.Fatal(err)
	}
	s := newTGStandin(t, tok, tgDir, regDir)
	prep := tgButton(t, s.send(tgCommand("/status")), "Prepare resume ")
	review := s.send(tgTap(prep))
	again := tgButton(t, review, "Start ")
	cb, ok := telegram.DecodeControlCallback(again)
	if !ok || cb.Action != telegram.ActionRestart {
		t.Fatalf("expected ActionRestart, got %+v", cb)
	}
	// Forge ActionResume directly without tapping ActionRestart first.
	forged, err := telegram.EncodeControlCallback(telegram.ControlCallback{
		Action:         telegram.ActionResume,
		RegistrationID: cb.RegistrationID,
		TicketID:       cb.TicketID,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply := s.send(tgTap(forged))
	if len(reply) == 0 || !strings.Contains(reply[0].Text, "confirm the start-again prompt first") {
		t.Fatalf("expected refusal message, got %+v", reply)
	}
	if c, _ := control.Load(entry.StateDir); !c.Stop || !c.AwaitingUser {
		t.Fatalf("forged ActionResume cleared holds: %+v", c)
	}
}

func TestTelegramRedactsThreadSlugLoopNames(t *testing.T) {
	const tok = "123456789:EXAMPLE_FIRST_USE_TOKEN"
	tgDir := setupPairedTelegramDir(t, tok, time.Now().UTC())
	regDir, entry := clientFixture(t)
	// Rename loop in both registry and config to a raw bb-thr- thread slug.
	for _, p := range []string{filepath.Join(regDir, "registry.json"), entry.ConfigPath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), entry.Loop, "bb-thr-example999")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := control.Save(entry.StateDir, control.File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	s := newTGStandin(t, tok, tgDir, regDir)
	statusMsgs := s.send(tgCommand("/status"))
	if len(statusMsgs) == 0 || strings.Contains(statusMsgs[0].Text, "bb-thr-example999") || !strings.Contains(statusMsgs[0].Text, "A loop") {
		t.Fatalf("status leaked thread slug: %+v", statusMsgs)
	}
	prep := tgButton(t, statusMsgs, "Prepare resume ")
	if prep == "" {
		t.Fatalf("missing prepare resume button: %+v", statusMsgs)
	}
	review := s.send(tgTap(prep))
	if len(review) == 0 || strings.Contains(review[0].Text, "bb-thr-example999") || !strings.Contains(review[0].Text, "A loop") {
		t.Fatalf("review leaked thread slug: %+v", review)
	}
	confirm := tgButton(t, review, "Confirm resume ")
	if confirm == "" {
		t.Fatalf("missing confirm resume button: %+v", review)
	}
	resumed := s.send(tgTap(confirm))
	if len(resumed) == 0 || strings.Contains(resumed[0].Text, "bb-thr-example999") || !strings.Contains(resumed[0].Text, "A loop") {
		t.Fatalf("alert leaked thread slug: %+v", resumed)
	}
}

func TestTelegramPollerLastExitInServiceStatus(t *testing.T) {
	tgDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv(telegram.TokenEnv, "")
	var out, errb bytes.Buffer
	code := telegramCommand(env{ctx: context.Background(), stdout: &out, stderr: &errb}, []string{"poll", "--once", "--dir", tgDir})
	if code == exitOK {
		t.Fatalf("expected unpaired poll to fail")
	}
	out.Reset()
	errb.Reset()
	telegramServiceGOOS = "linux"
	if code := telegramCommand(env{ctx: context.Background(), stdout: &out, stderr: &errb}, []string{"service", "status", "--dir", tgDir}); code != exitOK {
		t.Fatalf("service status: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Last exit: not paired") {
		t.Fatalf("expected Last exit line, got: %s", out.String())
	}
}
