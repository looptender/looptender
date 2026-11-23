package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
)

// sessionpipeMinVersion is the oldest sessionpipe whose tier-0 file sink and
// status JSON this target was written against.
const sessionpipeMinVersion = "0.18.0"

// sessionpipeObservedNotice is what doctor, status and the dock say about an
// observed target.
const sessionpipeObservedNotice = "Observed, never woken: looptender reads this session's tier-0 sessionpipe events and never sends to, wakes, re-prompts or stops it. Pause and resume change only whether looptender checks it; a stop ends the checks and leaves the session alone."

// sessionpipeSinkCommand is the one line the person runs to add the sink.
func sessionpipeSinkCommand() string {
	return "sessionpipe sink add file:PATH --tier 0 --name looptender"
}

// doctorSessionpipe checks an observed sessionpipe target. It runs only
// `sessionpipe --version` and `sessionpipe status --json`, both read-only, and
// never prints their output, the events file's path or anything in it.
func doctorSessionpipe(ctx context.Context, r *doctorReport, prefix string, s *config.Sessionpipe, offline bool, timeout time.Duration, run doctorRun) {
	r.add(prefix+".observed", "ok", sessionpipeObservedNotice, "")
	harness, _, _ := config.SplitSessionpipeRef(s.Session)
	doctorSessionpipeFile(r, prefix+".events_file", s.EventsFile)
	r.add(prefix+".limits", "unavailable", "sessionpipe reports usage limits and error classes only at tier 1, which also carries words and paths, so this target reads neither: an errored turn is of class unknown and no usage is read.", "")

	resolved, err := exec.LookPath(s.CLI)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
	}
	if err != nil {
		r.add(prefix+".cli", "warning", "The sessionpipe command was not found, so its version and sink were not checked. Checks do not run it; they read the events file only.", "Install sessionpipe or set sessionpipe.cli, then run doctor again.")
		return
	}
	r.add(prefix+".cli", "ok", "The sessionpipe command resolves. Doctor runs only its --version and status --json.", "")
	if offline || run == nil {
		r.add(prefix+".version", "unavailable", "sessionpipe version query skipped in offline mode.", "")
		r.add(prefix+".sink", "unavailable", "sessionpipe sink query skipped in offline mode.", "")
		return
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
	out, err = call("status", "--json")
	if err != nil {
		r.add(prefix+".sink", "unavailable", "sessionpipe status --json failed, timed out, or exceeded its output limit.", "Run sessionpipe doctor; raw output is withheld.")
		return
	}
	doctorSessionpipeStatus(r, prefix, out, harness, s.EventsFile)
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
func doctorSessionpipeStatus(r *doctorReport, prefix string, raw []byte, harness, eventsFile string) {
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
		return
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
		r.add(prefix+".sink", "error", "No sessionpipe file sink writes the events file, so the session will read as unknown.", "Add it once at this computer's terminal: "+sessionpipeSinkCommand()+", with PATH the absolute path in sessionpipe.events_file.")
	}
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
