package sessionpipe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/snapshot"
)

// A wake (sessionpipe.send: wake_only) is delivered with
//
//	sessionpipe ask <harness>:<id> --quiet --text -
//
// the prompt on stdin. sessionpipe's control daemon delivers it the way the
// session allows (a parked waiter, the next Stop, the session's inbox, a
// headless resume) and the command then waits for the agent's whole answer.
// looptender never reads that answer: stdout goes to the null device. It reads
// only how the command ends within AckWindow: a refusal names a code, which is
// kept from a fixed set; a command still running then is a delivery under way.
// The command keeps running detached until the agent has answered.

// AckWindow is how long Send waits for an ask to be refused before it counts
// as delivered.
const AckWindow = 3 * time.Second

// AskRecordFile is the record of the last wake, beside the reader's record.
const AskRecordFile = "sessionpipe-ask.json"

// askStale is when a recorded ask stops holding back the next one even if its
// process seems alive: a process ID can be reused.
const askStale = 24 * time.Hour

// maxStderr bounds what is read of the ask's stderr: enough for its one
// refusal line, never its progress (--quiet sends none).
const maxStderr = 4 << 10

// askRecord holds no text: when, which process, and how it ended.
type askRecord struct {
	PID int `json:"pid,omitempty"`
	// ProcStart is when that process started, as the system names it, so a
	// process ID reused after a restart is not taken for this wake.
	ProcStart string    `json:"procStart,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	// Outcome is delivering, answered, refused, failed or unknown.
	Outcome string `json:"outcome"`
	// Code is the refusal code, from refusalCodes, when refused.
	Code string `json:"code,omitempty"`
}

// refusalCodes are the codes sessionpipe 0.18 refuses an ask with. Anything
// else is "other", so no daemon text is ever kept.
var refusalCodes = map[string]bool{"ask_off": true, "folder": true, "harness": true, "permission": true, "start_limit": true,
	"copy_limit": true, "bad_fields": true, "loop": true, "busy": true, "unsupported": true, "depth": true}

var refusedRE = regexp.MustCompile(`sessionpipe ask: refused \(([a-z_]{1,32})\)`)

// askEnvNames is everything the ask is given of the supervisor's
// environment: where to find node and the user's files, the locale and time
// zone, sessionpipe's own location settings, and what Windows needs to run a
// program. No token, no session identity (sessionpipe takes the asking
// session from CLAUDE_CODE_SESSION_ID and its chain from
// SESSIONPIPE_ASK_CHAIN), and no test switch reaches it.
var askEnvNames = []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TZ", "LANG", "LC_ALL", "LC_CTYPE",
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR",
	"SESSIONPIPE_CONFIG", "SESSIONPIPE_STATE", "SESSIONPIPE_SOCKET", "SESSIONPIPE_DATA",
	"SYSTEMROOT", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"}

// askEnv is the allow-listed environment for the ask.
func askEnv(env []string) []string {
	keep := map[string]bool{}
	for _, n := range askEnvNames {
		keep[n] = true
	}
	var out []string
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && keep[name] {
			out = append(out, kv)
		}
	}
	return out
}

// PendingWake reports whether the last wake recorded in a target's state
// directory is still running, and since when. A wake is pending while its
// process, the same one by process ID and start time, is alive and it is
// under a day old. It reads the record only.
func PendingWake(dir string, now time.Time) (since time.Time, pending bool) {
	a := &Adapter{dir: dir}
	r, ok := a.loadAsk()
	if !ok || r.Outcome != "delivering" || now.Sub(r.StartedAt) >= askStale || !alive(r.PID) {
		return time.Time{}, false
	}
	if r.ProcStart != "" {
		// Another start time: the process ID now names another process. A
		// start time that cannot be read now says nothing either way, so
		// the wake stays pending, up to askStale.
		if now := readProcStart(r.PID); now != "" && now != r.ProcStart {
			return time.Time{}, false
		}
	}
	return r.StartedAt, true
}

// readProcStart is procStart; tests replace it.
var readProcStart = procStart

func (a *Adapter) askPath() string { return filepath.Join(a.dir, AskRecordFile) }

func (a *Adapter) loadAsk() (askRecord, bool) {
	b, err := os.ReadFile(a.askPath())
	var r askRecord
	if err != nil || json.Unmarshal(b, &r) != nil {
		return askRecord{}, false
	}
	return r, true
}

func (a *Adapter) saveAsk(r askRecord) error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return snapshot.WriteFileAtomic(a.askPath(), b, 0o600, false)
}

// Send delivers a wake when the loop opted in (send: wake_only). A queued
// message (a nudge) is never sent: only an idle session is woken.
func (a *Adapter) Send(ctx context.Context, prompt string, mode adapter.Mode) error {
	if !a.wake {
		return adapter.ErrUnsupported{Op: "sending to an observed sessionpipe session"}
	}
	if mode == adapter.ModeQueue {
		return adapter.ErrUnsupported{Op: "queueing a message behind a sessionpipe turn"}
	}
	if strings.TrimSpace(prompt) == "" {
		return adapter.ErrNotStarted{Reason: "the wake text is empty"}
	}
	if len(prompt) > 20000 {
		return adapter.ErrNotStarted{Reason: "the wake text is over the 20,000 characters sessionpipe ask carries"}
	}
	if _, pending := PendingWake(a.dir, a.now()); pending {
		return adapter.ErrNotStarted{Reason: "the previous wake is still being delivered or answered"}
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return adapter.ErrNotStarted{Reason: "the target's state directory cannot be written"}
	}
	cmd := exec.Command(a.cli, "ask", a.harness+":"+a.id, "--quiet", "--text", "-")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = nil // the answer goes to the null device, unread
	env := askEnv(os.Environ())
	if a.extraEnv != nil {
		env = append(env, a.extraEnv...)
	}
	cmd.Env = env
	// Stderr is read through a pipe, at most maxStderr bytes kept in memory
	// and the rest discarded: nothing of it is written anywhere.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return adapter.ErrNotStarted{Reason: "sessionpipe could not be run"}
	}
	detach(cmd)
	started := a.now()
	if err := cmd.Start(); err != nil {
		return adapter.ErrNotStarted{Reason: "sessionpipe could not be run: " + pathless(err).Error()}
	}
	rec := askRecord{PID: cmd.Process.Pid, ProcStart: readProcStart(cmd.Process.Pid), StartedAt: started, Outcome: "delivering"}
	if err := a.saveAsk(rec); err != nil {
		// The ask is running; without its record the next wake cannot know.
		_ = cmd.Process.Kill()
		_, _ = io.Copy(io.Discard, stderr)
		_ = cmd.Wait()
		return adapter.ErrNotStarted{Reason: "the wake's record cannot be saved"}
	}
	type result struct {
		err    error
		stderr []byte
	}
	done := make(chan result, 1)
	go func() {
		head, _ := io.ReadAll(io.LimitReader(stderr, maxStderr))
		_, _ = io.Copy(io.Discard, stderr)
		done <- result{cmd.Wait(), head}
	}()
	window := a.ackWindow
	if window <= 0 {
		window = AckWindow
	}
	select {
	case r := <-done:
		return a.ended(rec, r.err, r.stderr)
	case <-time.After(window):
		// Accepted and under way: the daemon holds it now. Waiting in the
		// background reaps the process if this one lives that long.
		go func() {
			r := <-done
			_ = a.ended(rec, r.err, r.stderr)
		}()
		return nil
	case <-ctx.Done():
		// The check is ending; the ask keeps going, and its record says so.
		go func() { <-done }()
		return nil
	}
}

// ended records how the ask finished and says what it means for this send.
func (a *Adapter) ended(rec askRecord, waitErr error, stderr []byte) error {
	code := 0
	var exit *exec.ExitError
	switch {
	case waitErr == nil:
	case errors.As(waitErr, &exit):
		code = exit.ExitCode()
	default:
		code = -1
	}
	var result error
	switch code {
	case 0:
		rec.Outcome = "answered"
	case 1:
		if c := refusal(stderr); c != "" {
			rec.Outcome, rec.Code = "refused", c
			result = adapter.ErrNotStarted{Reason: "sessionpipe refused the wake (" + c + ")"}
		} else {
			rec.Outcome = "failed"
			result = errors.New("sessionpipe ask failed; whether the wake reached the session is unknown")
		}
	case 2:
		rec.Outcome = "refused"
		rec.Code = "usage"
		result = adapter.ErrNotStarted{Reason: "sessionpipe did not accept the ask command"}
	default:
		rec.Outcome = "unknown"
		result = fmt.Errorf("sessionpipe ask ended with code %d; whether the wake reached the session is unknown", code)
	}
	rec.PID, rec.ProcStart = 0, ""
	_ = a.saveAsk(rec)
	return result
}

// refusal reads the head of the ask's stderr for its one refusal line and
// returns the code, from the fixed set, or "" when it was not a refusal.
// Nothing else of stderr is kept.
func refusal(b []byte) string {
	if bytes.Contains(b, []byte("no control daemon is answering")) {
		return "no_daemon"
	}
	m := refusedRE.FindSubmatch(b)
	if m == nil {
		return ""
	}
	if c := string(m[1]); refusalCodes[c] {
		return c
	}
	return "other"
}
