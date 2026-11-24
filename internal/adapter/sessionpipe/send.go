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
	PID       int       `json:"pid,omitempty"`
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

// scrubbedEnv is the supervisor's environment without what would make the
// ask look like it came from a session: sessionpipe takes the asking session
// from these and would put this wake in a delegation chain.
func scrubbedEnv(env []string) []string {
	drop := []string{"CLAUDE_CODE_SESSION_ID=", "CODEX_SESSION_ID=", "SESSIONPIPE_ASK_CHAIN=", "SESSIONPIPE_ASKED_BY=", "SESSIONPIPE_LAUNCHED_BY=", "SESSIONPIPE_CONTROL_JOB="}
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		for _, d := range drop {
			if strings.HasPrefix(kv, d) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

func (a *Adapter) askPath() string    { return filepath.Join(a.dir, AskRecordFile) }
func (a *Adapter) stderrPath() string { return filepath.Join(a.dir, "sessionpipe-ask.stderr") }

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
	if prev, ok := a.loadAsk(); ok && prev.Outcome == "delivering" && a.now().Sub(prev.StartedAt) < askStale && alive(prev.PID) {
		return adapter.ErrNotStarted{Reason: "the previous wake is still being delivered or answered"}
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return adapter.ErrNotStarted{Reason: "the target's state directory cannot be written"}
	}
	stderr, err := os.OpenFile(a.stderrPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return adapter.ErrNotStarted{Reason: "the target's state directory cannot be written"}
	}
	defer stderr.Close()
	cmd := exec.Command(a.cli, "ask", a.harness+":"+a.id, "--quiet", "--text", "-")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = nil // the answer goes to the null device, unread
	cmd.Stderr = stderr
	cmd.Env = scrubbedEnv(os.Environ())
	detach(cmd)
	started := a.now()
	if err := cmd.Start(); err != nil {
		return adapter.ErrNotStarted{Reason: "sessionpipe could not be run: " + pathless(err).Error()}
	}
	rec := askRecord{PID: cmd.Process.Pid, StartedAt: started, Outcome: "delivering"}
	if err := a.saveAsk(rec); err != nil {
		// The ask is running; without its record the next wake cannot know.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return adapter.ErrNotStarted{Reason: "the wake's record cannot be saved"}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	window := a.ackWindow
	if window <= 0 {
		window = AckWindow
	}
	select {
	case err := <-done:
		return a.ended(rec, err)
	case <-time.After(window):
		// Accepted and under way: the daemon holds it now. Waiting in the
		// background reaps the process if this one lives that long.
		go func() {
			err := <-done
			_ = a.ended(rec, err)
		}()
		return nil
	case <-ctx.Done():
		// The check is ending; the ask keeps going, and its record says so.
		go func() { <-done }()
		return nil
	}
}

// ended records how the ask finished and says what it means for this send.
func (a *Adapter) ended(rec askRecord, waitErr error) error {
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
		if c := a.refusal(); c != "" {
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
	rec.PID = 0
	_ = a.saveAsk(rec)
	// Whatever else the ask wrote (a failure's detail) is not kept.
	_ = os.Truncate(a.stderrPath(), 0)
	return result
}

// refusal reads the ask's stderr for its one refusal line and returns the
// code, from the fixed set, or "" when it was not a refusal. Nothing else of
// stderr is kept: the file is emptied after reading.
func (a *Adapter) refusal() string {
	f, err := os.Open(a.stderrPath())
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, maxStderr))
	f.Close()
	_ = os.Truncate(a.stderrPath(), 0)
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
