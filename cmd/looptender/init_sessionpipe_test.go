package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
)

const spSession = "claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e"

func noCLI(string) (string, error) { return "", os.ErrNotExist }

func TestInitSessionpipeNonInteractive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "setup")
	var out, errOut bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errOut}
	code := setupLoop(e, []string{"--non-interactive", "--agent", "sessionpipe", "--name", "observed-example", "--session", spSession, "--directory", dir}, strings.NewReader(""), noCLI)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	cfg, err := config.Load(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	// The setup directory as init canonicalised it (a temporary directory
	// may be behind a link).
	events := filepath.Join(filepath.Dir(cfg.Loop.StateDir), "sessionpipe-events.jsonl")
	a := cfg.Loop.Agent
	if a.Adapter != config.SessionpipeAdapter || a.Sessionpipe.Session != spSession || a.Sessionpipe.EventsFile != events || cfg.Loop.Prompt != "" || cfg.Loop.PromptFile != "" {
		t.Fatalf("config: %+v %+v", a, a.Sessionpipe)
	}
	if want := "sessionpipe sink add " + commandQuote("file:"+events) + " --tier 0 --name looptender"; !strings.Contains(out.String(), "\n  "+want+"\n") {
		t.Fatalf("the sink line is not printed exactly:\n%s", out.String())
	}
	for _, want := range []string{"never sends to, wakes", " doctor -c ", " start --foreground -c "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows" {
		t.Errorf("setup directory not private: %v %v", info, err)
	}
	if _, err := os.Stat(events); !os.IsNotExist(err) {
		t.Error("init created the events file; sessionpipe creates it")
	}

	// JSON, and a dry run writes nothing.
	out.Reset()
	dry := filepath.Join(t.TempDir(), "dry")
	ev := filepath.Join(t.TempDir(), "events.jsonl")
	if code := setupLoop(e, []string{"--non-interactive", "--json", "--dry-run", "--agent", "sessionpipe", "--name", "observed-example", "--session", spSession, "--events-file", ev, "--directory", dry}, strings.NewReader(""), noCLI); code != exitOK {
		t.Fatalf("json: exit %d: %s", code, errOut.String())
	}
	var r initSessionpipeReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Outcome != "dry_run" || !r.Observed || r.EventsFile != ev || r.SinkCommand != sessionpipeSinkLine(ev) || len(r.Next) != 3 {
		t.Fatalf("json: %+v %v\n%s", r, err, out.String())
	}
	if _, err := os.Stat(dry); !os.IsNotExist(err) {
		t.Error("a dry run wrote the setup")
	}
}

func TestInitSessionpipeInteractive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "setup")
	var out, errOut bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errOut}
	input := strings.NewReader(spSession + "\n\n\n")
	if code := setupLoop(e, []string{"--agent", "sessionpipe", "--directory", dir}, input, noCLI); code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut.String(), out.String())
	}
	cfg, err := config.Load(filepath.Join(dir, config.FileName))
	if err != nil || cfg.Loop.Name != "observed-claude-code-b3bc2e23" || cfg.Loop.Agent.Sessionpipe.Session != spSession {
		t.Fatalf("%v %+v", err, cfg)
	}
}

func TestInitSessionpipeRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"a task":             {"--agent", "sessionpipe", "--name", "x", "--session", spSession, "--task", "do things"},
		"a fallback":         {"--agent", "sessionpipe", "--name", "x", "--session", spSession, "--fallback", "codex:pool"},
		"no session":         {"--agent", "sessionpipe", "--name", "x"},
		"no session or name": {"--agent", "sessionpipe"},
		"a bad session":      {"--agent", "sessionpipe", "--name", "x", "--session", "a title"},
		"session on claude":  {"--agent", "claude", "--name", "x", "--task", "t", "--session", spSession},
		"events file on bb":  {"--agent", "bb", "--name", "x", "--task", "t", "--events-file", "/x"},
	} {
		var out, errOut bytes.Buffer
		e := env{ctx: context.Background(), stdout: &out, stderr: &errOut}
		args = append([]string{"--non-interactive", "--directory", filepath.Join(t.TempDir(), "setup")}, args...)
		if code := setupLoop(e, args, strings.NewReader(""), noCLI); code == exitOK {
			t.Errorf("%s: accepted", name)
		}
	}
}

// doctor walks the setup in order and its last check names the first step
// that failed, with what to do.
func TestDoctorSessionpipeWalksTheSteps(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "events.jsonl")
	s := &config.Sessionpipe{Session: spSession, EventsFile: file, CLI: os.Args[0]}
	ids := func(r doctorReport) []string {
		var out []string
		for _, c := range r.Checks {
			out = append(out, strings.TrimPrefix(c.ID, "agent.primary."))
		}
		return out
	}
	walk := func(f *fakeSessionpipe) (doctorReport, doctorCheck) {
		var r doctorReport
		doctorSessionpipe(context.Background(), &r, "agent.primary", s, false, 0, f.run)
		return r, r.Checks[len(r.Checks)-1]
	}
	// No sink yet: the sink is the step to take, before the file.
	r, next := walk(&fakeSessionpipe{version: "0.18.2", status: sessionpipeStatusJSON("file:/elsewhere", 0, "null", "null", "current")})
	if got := strings.Join(ids(r), ","); got != "observed,cli,version,hooks,sink,pool,events_file,limits,next" {
		t.Fatalf("order: %s", got)
	}
	if next.Status != "error" || !strings.Contains(next.Message, "(sink)") || !strings.Contains(next.Fix, "sessionpipe sink add") {
		t.Fatalf("next: %+v", next)
	}
	// Sink added, no event yet: a warning that says to use the session.
	_, next = walk(&fakeSessionpipe{version: "0.18.2", status: sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current")})
	if next.Status != "warning" || !strings.Contains(next.Message, "(events_file)") || !strings.Contains(next.Fix, "Send the session one message") {
		t.Fatalf("next before the first event: %+v", next)
	}
	// Old sessionpipe comes first.
	_, next = walk(&fakeSessionpipe{version: "0.17.0", status: sessionpipeStatusJSON("file:/elsewhere", 0, "null", "null", "current")})
	if !strings.Contains(next.Message, "(version)") || !strings.Contains(next.Fix, "sessionpipe update") {
		t.Fatalf("old version: %+v", next)
	}
	// All well.
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, next = walk(&fakeSessionpipe{version: "0.18.2", status: sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current")})
	if next.Status != "ok" {
		t.Fatalf("all well: %+v", next)
	}
}

// Without --name, the loop is named after the session, so the shortest
// command works.
func TestInitSessionpipeNamesTheLoopAfterTheSession(t *testing.T) {
	var out, errOut bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errOut}
	dry := filepath.Join(t.TempDir(), "dry")
	if code := setupLoop(e, []string{"--non-interactive", "--json", "--dry-run", "--agent", "sessionpipe", "--session", "example:demo", "--directory", dry}, strings.NewReader(""), noCLI); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var r initSessionpipeReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Loop != "observed-example-2a97516c" {
		t.Fatalf("%+v %v", r, err)
	}
	for session, want := range map[string]string{
		spSession:                        "observed-claude-code-b3bc2e23",
		"codex:01A10860-a43f":            "observed-codex-f623c93a",
		"gemini-cli:...":                 "observed-gemini-cli-ab5df625",
		"not a session":                  "observed-session",
		strings.Repeat("h", 64) + ":abc": "observed-" + strings.Repeat("h", 46) + "-ba7816bf",
	} {
		got := sessionpipeLoopName(session)
		if got != want {
			t.Errorf("%q: %q, want %q", session, got, want)
		}
		if len(got) > 64 || strings.Trim(got, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			t.Errorf("%q: %q is not a valid loop name", session, got)
		}
	}
	// Time-ordered ids of sessions started together share their first
	// characters; their names must still differ, and carry none of the id.
	a, b := "codex:01a10860-a43f-7ce2-b12f-a7911ae8a880", "codex:01a10860-a43f-7ce2-b12f-a7911ae8a881"
	if na, nb := sessionpipeLoopName(a), sessionpipeLoopName(b); na == nb || strings.Contains(na, "01a10860") || na != "observed-codex-57737761" || nb != "observed-codex-3572b0c2" {
		t.Errorf("two sessions sharing their id's start: %q and %q", na, nb)
	}
}
