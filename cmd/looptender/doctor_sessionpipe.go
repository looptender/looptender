package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/adapter/sessionpipe"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// sessionpipeMinVersion is the oldest sessionpipe whose tier-0 file sink and
// status JSON this target was written against.
const sessionpipeMinVersion = "0.18.0"

// sessionpipeObservedNotice is what doctor, status and the dock say about an
// observed target.
const sessionpipeObservedNotice = "Observed, never woken: looptender reads this session's tier-0 sessionpipe events and never sends to, wakes, re-prompts or stops it. Pause and resume change only whether looptender checks it; nothing is ever sent. A stop ends the checks and leaves the session alone."

// sessionpipeWakeNotice is the same for a loop with sessionpipe.send:
// wake_only.
const sessionpipeWakeNotice = "Observed, woken only when idle: looptender reads this session's tier-0 sessionpipe events and, because this loop opted in, wakes it through sessionpipe ask when it is between turns, within the loop's send limits and holds. It never nudges, re-prompts or stops it, and never reads the answer. Pause stops checks and wakes; resume starts them again. A stop ends the checks and leaves the session alone."

// observedNotice is the sentence for an observed agent.
func observedNotice(wakes bool) string {
	if wakes {
		return sessionpipeWakeNotice
	}
	return sessionpipeObservedNotice
}

// sessionpipeSinkCommand is the one line the person runs to add the sink.
func sessionpipeSinkCommand() string {
	return "sessionpipe sink add file:PATH --tier 0 --name looptender"
}

// doctorSessionpipe checks an observed sessionpipe target. It runs only
// `sessionpipe --version` and `sessionpipe status --json`, both read-only, and
// never prints their output, the events file's path or anything in it.
//
// The checks run in the order a person sets the target up: the sessionpipe
// command, its version, its hooks for the session's harness, the file sink,
// then the events file. The last check, next, names the first that failed and
// its next step, or says every check passed.
func doctorSessionpipe(ctx context.Context, r *doctorReport, prefix string, s *config.Sessionpipe, offline bool, timeout time.Duration, run doctorRun) {
	r.add(prefix+".observed", "ok", observedNotice(s.Send == config.SessionpipeSendWakeOnly), "")
	start := len(r.Checks)
	sink := doctorSessionpipeTool(ctx, r, prefix, s, offline, timeout, run)
	doctorSessionpipeFileStep(r, prefix+".events_file", s.EventsFile, sink)
	r.add(prefix+".limits", "unavailable", "sessionpipe reports usage limits and error classes only at tier 1, which also carries words and paths, so this target reads neither: an errored turn is of class unknown and no usage is read.", "")
	doctorSessionpipeNext(r, prefix, start)
}

// sinkState is what doctor learned about the file sink: found, not found, or
// not checked (no command, offline, or unreadable status).
type sinkState int

const (
	sinkUnchecked sinkState = iota
	sinkFound
	sinkMissing
)

// doctorSessionpipeNext adds the walk's last check: the first failed step
// (an error before a warning) and what to do about it.
func doctorSessionpipeNext(r *doctorReport, prefix string, start int) {
	for _, want := range []string{"error", "warning"} {
		for i, c := range r.Checks[start:] {
			if c.Status != want || c.Fix == "" {
				continue
			}
			step := strings.TrimPrefix(c.ID, prefix+".")
			r.add(prefix+".next", want, fmt.Sprintf("Step %d of the sessionpipe setup (%s) needs attention: %s", i+1, step, c.Message), c.Fix)
			return
		}
	}
	r.add(prefix+".next", "ok", "Every sessionpipe check that could run passed. Start supervision with start and this configuration; nothing is ever sent to the session.", "")
}

// doctorSessionpipeTool checks the sessionpipe command, its version, its hooks
// and the file sink.
func doctorSessionpipeTool(ctx context.Context, r *doctorReport, prefix string, s *config.Sessionpipe, offline bool, timeout time.Duration, run doctorRun) sinkState {
	harness, _, _ := config.SplitSessionpipeRef(s.Session)
	resolved, err := exec.LookPath(s.CLI)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
	}
	if err != nil {
		r.add(prefix+".cli", "warning", "The sessionpipe command was not found, so its version and sink were not checked. Checks do not run it; they read the events file only.", "Install sessionpipe (npx sessionpipe install) or set sessionpipe.cli, then run doctor again.")
		return sinkUnchecked
	}
	r.add(prefix+".cli", "ok", "The sessionpipe command resolves. Doctor runs only its --version and status --json.", "")
	if offline || run == nil {
		r.add(prefix+".version", "unavailable", "sessionpipe version query skipped in offline mode.", "")
		r.add(prefix+".sink", "unavailable", "sessionpipe sink query skipped in offline mode.", "")
		return sinkUnchecked
	}
	call := func(args ...string) ([]byte, error) {
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return run(cmdCtx, resolved, args, nil, "")
	}
	out, err := call("--version")
	switch v := doctorVersion(out); {
	case err != nil:
		r.add(prefix+".version", "unavailable", "sessionpipe --version failed, timed out, or exceeded its output limit.", "Check the installed sessionpipe; raw output is withheld.")
	case v == "":
		r.add(prefix+".version", "unavailable", "sessionpipe returned an unrecognized version format; raw output is withheld.", "Check the installed sessionpipe version manually.")
	case versionBefore(v, sessionpipeMinVersion):
		r.add(prefix+".version", "error", "sessionpipe "+v+" is older than "+sessionpipeMinVersion+", the oldest version this target reads.", "Update sessionpipe (sessionpipe update), then run doctor again.")
	default:
		r.add(prefix+".version", "ok", "sessionpipe "+v+" (this target needs "+sessionpipeMinVersion+" or later).", "")
	}
	if s.Send == config.SessionpipeSendWakeOnly {
		out, err := call("ask", "status")
		doctorSessionpipeAsk(r, prefix+".send", out, err)
	}
	out, err = call("status", "--json")
	if err != nil {
		r.add(prefix+".sink", "unavailable", "sessionpipe status --json failed, timed out, or exceeded its output limit.", "Run sessionpipe doctor; raw output is withheld.")
		return sinkUnchecked
	}
	return doctorSessionpipeStatus(r, prefix, out, harness, s.EventsFile)
}

// doctorSessionpipeFileStep checks the events file. A missing file with a
// sink already writing it only means no event has been written yet.
func doctorSessionpipeFileStep(r *doctorReport, id, path string, sink sinkState) {
	if _, err := os.Stat(path); os.IsNotExist(err) && sink == sinkFound {
		r.add(id, "warning", "The file sink is set up, but sessionpipe has not written the events file yet: it does so with the session's next event.", "Send the session one message (or start it), then run doctor again.")
		return
	}
	doctorSessionpipeFile(r, id, path)
}

// doctorSessionpipeFile checks that the events file exists and that nobody
// else can write it: anyone who can write it can make the session look
// waiting, ended or idle.
func doctorSessionpipeFile(r *doctorReport, id, path string) {
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		r.add(id, "error", "The sessionpipe events file does not exist yet. sessionpipe creates it with the first event after the sink is added.", "Add the sink once at this computer's terminal: "+sessionpipeSinkCommand()+", with PATH the absolute path in sessionpipe.events_file.")
		return
	case err != nil:
		r.add(id, "error", "The sessionpipe events file is inaccessible.", "Check its access permissions.")
		return
	case !info.Mode().IsRegular():
		r.add(id, "error", "The sessionpipe events file is not a regular file.", "Point sessionpipe.events_file at the file sink's file.")
		return
	}
	if runtime.GOOS == "windows" {
		doctorACL(r, id+".acl", path, false, doctorACLMetadata)
		r.add(id, "unavailable", "The events file exists; Windows mode bits do not establish who can write it. See the separate DACL metadata check.", "Keep it in a folder only your account can write.")
		return
	}
	switch perm := info.Mode().Perm(); {
	case perm&0o022 != 0:
		r.add(id, "error", "Other users can write the sessionpipe events file, and so could make the session look waiting, ended or idle.", "Remove group and other write access (chmod go-w), and keep it in a folder only you can write. Doctor never changes permissions.")
	case perm&0o044 != 0:
		r.add(id, "warning", "Other users can read the sessionpipe events file. It holds tier-0 events: session state, folder, branch and title.", "Keep it private (chmod 600) or in a folder only you can read. Doctor never changes permissions.")
	default:
		r.add(id, "ok", "The sessionpipe events file exists and only its owner can read or write it.", "")
	}
}

// doctorSessionpipeStatus reads sessionpipe status --json: the hooks for the
// session's harness, and a file sink at tier 0 writing the events file.
func doctorSessionpipeStatus(r *doctorReport, prefix string, raw []byte, harness, eventsFile string) sinkState {
	var st struct {
		Harnesses map[string][]struct {
			State string `json:"state"`
		} `json:"harnesses"`
		Sinks []struct {
			URL     string          `json:"url"`
			Tier    json.Number     `json:"tier"`
			MaxTier *json.Number    `json:"max_tier"`
			Paused  json.RawMessage `json:"paused"`
		} `json:"sinks"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &st) != nil || st.Harnesses == nil {
		r.add(prefix+".sink", "unavailable", "sessionpipe status --json returned an unrecognized shape; raw output is withheld.", "Run sessionpipe doctor.")
		return sinkUnchecked
	}
	hooks, ok := st.Harnesses[harness]
	switch {
	case !ok || len(hooks) == 0:
		r.add(prefix+".hooks", "error", "sessionpipe has no hooks for "+harness+" on this computer, so the session reports nothing.", "Run sessionpipe install, or sessionpipe doctor to see why.")
	default:
		current := true
		for _, h := range hooks {
			current = current && h.State == "current"
		}
		if current {
			r.add(prefix+".hooks", "ok", "sessionpipe's hooks for "+harness+" are current.", "")
		} else {
			r.add(prefix+".hooks", "warning", "sessionpipe's hooks for "+harness+" are missing or not current, so some events may not be written.", "Run sessionpipe doctor and the command it gives.")
		}
	}
	found := false
	for _, s := range st.Sinks {
		if !strings.HasPrefix(s.URL, "file:") || !sameSessionpipePath(strings.TrimPrefix(s.URL, "file:"), eventsFile) {
			continue
		}
		found = true
		tier, err := s.Tier.Float64()
		if err != nil {
			tier = 0
		}
		if s.MaxTier != nil {
			if m, err := s.MaxTier.Float64(); err == nil && m < tier {
				tier = m
			}
		}
		switch {
		case int(tier) != 0:
			r.add(prefix+".sink", "error", "The file sink writing the events file sends tier "+strconv.Itoa(int(tier))+", which carries words and paths. This target reads only tier 0 and refuses the file.", "Add it again at tier 0: "+sessionpipeSinkCommand()+", and remove the old file.")
		case len(s.Paused) > 0 && string(s.Paused) != "null" && string(s.Paused) != "false":
			r.add(prefix+".sink", "warning", "The file sink writing the events file is paused, so the session will read as unknown.", "Resume the sink in sessionpipe; doctor changes nothing there.")
		default:
			r.add(prefix+".sink", "ok", "A tier-0 sessionpipe file sink writes the events file.", "")
		}
	}
	if !found {
		r.add(prefix+".sink", "error", "No sessionpipe file sink writes the events file, so the session will read as unknown.", "Add it once at this computer's terminal: run the sessionpipe sink add line that looptender init printed, or "+sessionpipeSinkCommand()+" with PATH the absolute path in sessionpipe.events_file.")
		return sinkMissing
	}
	return sinkFound
}

// sameSessionpipePath compares a sink's path, as sessionpipe keeps it (a
// leading ~ for the home directory), with the configured path.
func sameSessionpipePath(sink, configured string) bool {
	if sink == "~" || strings.HasPrefix(sink, "~/") || strings.HasPrefix(sink, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		sink = filepath.Join(home, sink[1:])
	}
	if !filepath.IsAbs(sink) {
		return false
	}
	a, b := filepath.Clean(sink), filepath.Clean(configured)
	if ea, err := filepath.EvalSymlinks(a); err == nil {
		a = ea
	}
	if eb, err := filepath.EvalSymlinks(b); err == nil {
		b = eb
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

var semverCore = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// versionBefore reports whether v is older than min, by major, minor and
// patch only. A prerelease of min counts as min.
func versionBefore(v, min string) bool {
	parse := func(s string) [3]int {
		var out [3]int
		m := semverCore.FindStringSubmatch(s)
		if m == nil {
			return out
		}
		for i := range out {
			out[i], _ = strconv.Atoi(m[i+1])
		}
		return out
	}
	a, b := parse(v), parse(min)
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// doctorSessionpipeAsk checks what a wake needs from sessionpipe: asking on,
// and its background service running. It reads `sessionpipe ask status`, a
// page for a person, by its two fixed lines only; nothing else is printed.
func doctorSessionpipeAsk(r *doctorReport, id string, out []byte, err error) {
	if err != nil {
		r.add(id, "unavailable", "sessionpipe ask status failed, timed out, or exceeded its output limit, so whether a wake can be delivered is unknown.", "Run sessionpipe ask status yourself.")
		return
	}
	var on, off, running, stopped bool
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "ask: on":
			on = true
		case strings.HasPrefix(l, "ask: off"):
			off = true
		case strings.HasPrefix(l, "daemon: running"):
			running = true
		case strings.HasPrefix(l, "daemon: not running"):
			stopped = true
		}
	}
	switch {
	case off:
		r.add(id, "error", "sessionpipe.send is wake_only, but asking is off in sessionpipe, so every wake would be refused (ask_off).", "Turn it on yourself, at this computer's terminal: sessionpipe ask on. looptender never runs it.")
	case stopped:
		r.add(id, "error", "sessionpipe.send is wake_only, but sessionpipe's background service is not running, so a wake cannot be delivered.", "Start it as sessionpipe describes (sessionpipe ask on installs it); looptender never starts it.")
	case on && running:
		r.add(id, "ok", "Wakes are on: asking is on in sessionpipe and its background service is running. A wake still needs the session's folder on sessionpipe control folders; a refusal says so by its code.", "")
	default:
		r.add(id, "unavailable", "sessionpipe ask status printed an unrecognized page, so whether a wake can be delivered is unknown; raw output is withheld.", "Run sessionpipe ask status yourself.")
	}
}

// sessionpipeWakeStatus is an outstanding wake, for status and doctor.
type sessionpipeWakeStatus struct {
	Since      time.Time `json:"since"`
	ForSeconds int64     `json:"forSeconds"`
	Text       string    `json:"text"`
}

// pendingSessionpipeWake reads the target's wake record; nil when no wake is
// running.
func pendingSessionpipeWake(stateDir, target string, now time.Time) *sessionpipeWakeStatus {
	since, pending := sessionpipe.PendingWake(filepath.Join(stateDir, "targets", target), now)
	if !pending {
		return nil
	}
	d := now.Sub(since).Round(time.Second)
	return &sessionpipeWakeStatus{Since: since, ForSeconds: int64(d / time.Second),
		Text: fmt.Sprintf("a wake has been running since %s (%s): the agent has not finished answering it, so the next wake waits. sessionpipe has no cancel; a wake still running after a day no longer holds the next one back.", since.Local().Format(time.RFC3339), d)}
}

// doctorSessionpipeWake reports an outstanding wake: a warning once it has
// run for an hour, since every later wake waits for it.
func doctorSessionpipeWake(r *doctorReport, id, stateDir string, now time.Time) {
	pw := pendingSessionpipeWake(stateDir, state.Primary, now)
	switch {
	case pw == nil:
		r.add(id, "ok", "No wake is outstanding.", "")
	case time.Duration(pw.ForSeconds)*time.Second >= time.Hour:
		r.add(id, "warning", "A wake has been outstanding for a long time: "+pw.Text, "Look at the session: sessionpipe delivered the wake and the agent is still answering, or the delivery is stuck in sessionpipe. Wakes resume once it ends.")
	default:
		r.add(id, "ok", "A wake is being answered: "+pw.Text, "")
	}
}
