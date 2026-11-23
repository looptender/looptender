package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/userdir"
	"gopkg.in/yaml.v3"
)

// sessionpipeSinkLine is the exact command the person runs once so that
// sessionpipe writes the tier-0 events file this target reads.
func sessionpipeSinkLine(eventsFile string) string {
	return "sessionpipe sink add " + commandQuote("file:"+eventsFile) + " --tier 0 --name looptender"
}

// sessionpipeLoopName is the default loop name for a session: "observed-",
// its harness and the first eight letters or digits of its id, so two
// sessions of one harness get different names. It is a valid loop name for
// any session config.SplitSessionpipeRef accepts.
func sessionpipeLoopName(session string) string {
	harness, id, ok := config.SplitSessionpipeRef(session)
	if !ok {
		return "observed-session"
	}
	var short []byte
	for _, c := range []byte(strings.ToLower(id)) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			short = append(short, c)
			if len(short) == 8 {
				break
			}
		}
	}
	name := "observed-" + harness
	if len(name) > 55 {
		name = strings.TrimRight(name[:55], "-")
	}
	if len(short) > 0 {
		name += "-" + string(short)
	}
	return name
}

// initSessionpipeReport is init --json for an observed sessionpipe target.
type initSessionpipeReport struct {
	SchemaVersion int      `json:"schemaVersion"`
	Version       string   `json:"version"`
	Outcome       string   `json:"outcome"` // created or dry_run
	Loop          string   `json:"loop"`
	Adapter       string   `json:"adapter"`
	Observed      bool     `json:"observed"`
	Session       string   `json:"session"`
	EventsFile    string   `json:"eventsFile"`
	ConfigPath    string   `json:"configPath"`
	StateDir      string   `json:"stateDir"`
	SinkCommand   string   `json:"sinkCommand"`
	Next          []string `json:"next"`
	Limitations   []string `json:"limitations"`
}

// setupSessionpipe sets up a loop on an observed sessionpipe session. It has
// no task, project or provider: it writes a configuration that reads one
// tier-0 events file, and prints the one sessionpipe command the person runs.
// It runs no sessionpipe command and changes nothing in sessionpipe.
func setupSessionpipe(e env, o initOptions, session, eventsFile string, input io.Reader, visited map[string]bool, asJSON bool) int {
	fail := func(format string, args ...any) int {
		e.errf("init: "+format, args...)
		return exitUsage
	}
	for _, f := range []string{"task", "task-file", "with-goal", "thread", "provider", "account-dir", "account", "group", "model", "routes", "fallback", "new-thread-provider", "project"} {
		if visited[f] {
			return fail("--%s does not apply to --agent sessionpipe: an observed session is sent nothing and has no routes", f)
		}
	}
	r := bufio.NewReader(input)
	if !o.nonInteractive {
		ask := func(flag, label, def string, value *string) error {
			if visited[flag] {
				return nil
			}
			fmt.Fprintf(e.stdout, "%s [%s]: ", label, def)
			s, err := readInitLine(e.ctx, r)
			if err != nil {
				return fmt.Errorf("input ended; no setup written (use --non-interactive for scripts): %w", err)
			}
			if s = strings.TrimSpace(s); s == "" {
				s = def
			}
			*value = s
			return nil
		}
		fmt.Fprintln(e.stdout, "An observed sessionpipe session: looptender reads its tier-0 events and never sends to, wakes or stops it.")
		if err := ask("session", "Session, as <harness>:<id> (session.id in `sessionpipe tail --tier 0 --json`)", "", &session); err != nil {
			return fail("%v", err)
		}
		if err := ask("name", "Loop name", sessionpipeLoopName(session), &o.name); err != nil {
			return fail("%v", err)
		}
	}
	if session == "" {
		return fail("--agent sessionpipe needs --session <harness>:<id>")
	}
	if o.name == "" {
		o.name = sessionpipeLoopName(session)
	}
	if o.name == "" || len(o.name) > 64 || strings.Trim(o.name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || o.name[0] == '-' {
		return fail("name must be 1-64 lowercase letters, digits, or hyphens, starting with a letter or digit")
	}
	if _, _, ok := config.SplitSessionpipeRef(session); !ok {
		return fail("--session must be <harness>:<id> as sessionpipe names the session, for example claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e")
	}
	var err error
	if o.directory == "" {
		if o.directory, err = userdir.Config("loops", o.name); err != nil {
			return fail("%v", err)
		}
	}
	if o.directory, err = canonicalFuturePath(o.directory); err != nil {
		return fail("%v", err)
	}
	if _, err := os.Lstat(o.directory); !errors.Is(err, os.ErrNotExist) {
		return fail("setup directory must be new: %s", o.directory)
	}
	if !o.nonInteractive && !visited["events-file"] {
		def := filepath.Join(o.directory, "sessionpipe-events.jsonl")
		fmt.Fprintf(e.stdout, "Events file sessionpipe will write [%s]: ", def)
		s, err := readInitLine(e.ctx, r)
		if err != nil && !errors.Is(err, io.EOF) {
			return fail("input ended; no setup written: %v", err)
		}
		if eventsFile = strings.TrimSpace(s); eventsFile == "" {
			eventsFile = def
		}
	}
	if eventsFile == "" {
		// Inside the new private setup directory, so only its owner can read
		// what sessionpipe writes there.
		eventsFile = filepath.Join(o.directory, "sessionpipe-events.jsonl")
	}
	if eventsFile, err = filepath.Abs(eventsFile); err != nil {
		return fail("%v", err)
	}
	loop := map[string]any{"name": o.name, "state_dir": filepath.Join(o.directory, "state"), "adapter": config.SessionpipeAdapter,
		"sessionpipe": map[string]any{"session": session, "events_file": eventsFile}}
	b, err := yaml.Marshal(map[string]any{"version": 1, "loop": loop})
	if err != nil {
		return fail("%v", err)
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return fail("%v", err)
	}
	cfgPath := filepath.Join(o.directory, config.FileName)
	sink := sessionpipeSinkLine(eventsFile)
	doctorCmd := initCommandPrefix() + " doctor -c " + commandQuote(cfgPath)
	startCmd := initCommandPrefix() + " start --foreground -c " + commandQuote(cfgPath)
	if !o.dryRun {
		if err := e.ctx.Err(); err != nil {
			e.errf("init: %v", err)
			return exitFailed
		}
		if _, err := writeInit(o.directory, map[string][]byte{config.FileName: b}, nil); err != nil {
			e.errf("init: %v", err)
			return exitFailed
		}
	}
	if asJSON {
		r := initSessionpipeReport{SchemaVersion: 1, Version: version, Outcome: "created", Loop: o.name, Adapter: config.SessionpipeAdapter, Observed: true,
			Session: session, EventsFile: eventsFile, ConfigPath: cfgPath, StateDir: cfg.Loop.StateDir, SinkCommand: sink,
			Next:        []string{sink, doctorCmd, startCmd},
			Limitations: []string{sessionpipeObservedNotice, "Setup runs no sessionpipe command and changes nothing in sessionpipe; the sink is yours to add.", "Error classes and usage limits are not read: sessionpipe sends them only above tier 0."}}
		if o.dryRun {
			r.Outcome = "dry_run"
		}
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			return exitFailed
		}
		return exitOK
	}
	fmt.Fprintf(e.stdout, "Loop: %s\nTarget: %s\nEvents file: %s\nConfiguration: %s\nState: %s\n", o.name, cfg.Loop.Agent.Describe(), eventsFile, cfgPath, cfg.Loop.StateDir)
	fmt.Fprintln(e.stdout, sessionpipeObservedNotice)
	if o.dryRun {
		fmt.Fprintln(e.stdout, "Dry run: no files written.")
		return exitOK
	}
	fmt.Fprintln(e.stdout, "Created private setup. Three steps to try it:")
	fmt.Fprintln(e.stdout, "1. Once, at this computer's own terminal, tell sessionpipe to write the events file (looptender does not run this for you):")
	fmt.Fprintf(e.stdout, "  %s\n", sink)
	fmt.Fprintln(e.stdout, "2. Check the setup; each failed check says what to do next:")
	fmt.Fprintf(e.stdout, "  %s\n", doctorCmd)
	fmt.Fprintln(e.stdout, "3. Start supervision (nothing is ever sent to the session):")
	fmt.Fprintf(e.stdout, "  %s\n", startCmd)
	return exitOK
}
