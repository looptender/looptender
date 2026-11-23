package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/envvar"
)

// A background daemon started inside a BB thread is a child of that
// thread's process, and quitting BB ends it. There it is handed to the
// user's own service manager instead, as a one-off job outside BB: launchd
// (launchctl bootstrap in the per-user background domain) on macOS, the user's systemd (systemd-run --user) on
// Linux. When that is not available it is started as before, with a
// warning that it stops when BB quits. Windows is not handled: whether a
// detached process survives BB quitting there is not established.

// backgroundLaunchEnv forces the child start ("child"); tests set it so no
// real service manager is used.
const backgroundLaunchEnv = "LOOPTENDER_BACKGROUND_LAUNCH"

// launchLabelEnv carries the job's label to the daemon and to any build it
// adopts, for its launch-job record (status and doctor show it).
const launchLabelEnv = "LOOPTENDER_LAUNCHD_LABEL"

// launchJobName is the daemon's record of the service-manager job it runs
// in, written at start and removed when it ends.
const launchJobName = "launch-job.json"

type launchPlan struct {
	Kind  string // launchd or systemd
	Label string
	// Target is the launchd job's service target, user/<uid>/<label>.
	Target string
	Argv   []string
	// Files are written before Argv runs: for launchd, the job's property
	// list and the script it runs, in the loop's state directory.
	Files map[string][]byte
}

// backgroundLaunch is the service-manager launch for this platform, or
// false when there is none to use.
//
// On macOS the job goes in the per-user background domain (launchctl
// bootstrap user/<uid>), not the GUI login domain that launchctl submit
// uses from a thread: the login domain is torn down at logout or when the
// window server crashes, and a submitted job has no property list to bring
// it back. A job in the background domain must say so in its property list
// (LimitLoadToSessionType Background, without which bootstrap refuses
// it). It runs at load and is not kept alive. It runs a script that runs
// the daemon and then, however the daemon ended (stopped, crashed, refused,
// an adopted older build, a launch path that no longer exists), removes its
// own files and boots its job out.
func backgroundLaunch(goos string, lookPath func(string) (string, error), label string, child []string, uid int, stateDir string) (launchPlan, bool) {
	switch goos {
	case "darwin":
		p, err := lookPath("launchctl")
		if err != nil {
			return launchPlan{}, false
		}
		target := fmt.Sprintf("user/%d/%s", uid, label)
		plist := filepath.Join(stateDir, "launch-"+label+".plist")
		script := filepath.Join(stateDir, "launch-"+label+".sh")
		quoted := make([]string, len(child))
		for i, a := range child {
			quoted[i] = launchQuote(a)
		}
		body := "#!/bin/sh\n# looptender: the launchd job " + label + ". It runs the daemon, then removes this job and its files, however the daemon ended.\n" +
			strings.Join(quoted, " ") + "\n" +
			"rm -f " + launchQuote(plist) + " " + launchQuote(script) + "\n" +
			"exec " + launchQuote(p) + " bootout " + launchQuote(target) + "\n"
		return launchPlan{Kind: "launchd", Label: label, Target: target, Argv: []string{p, "bootstrap", fmt.Sprintf("user/%d", uid), plist},
			Files: map[string][]byte{script: []byte(body), plist: launchPlist(label, script)}}, true
	case "linux":
		p, err := lookPath("systemd-run")
		if err != nil {
			return launchPlan{}, false
		}
		return launchPlan{Kind: "systemd", Label: label, Argv: append([]string{p, "--user", "--unit", label, "--collect", "--quiet", "--"}, child...)}, true
	}
	return launchPlan{}, false
}

// launchQuote single-quotes a word for the job's script, always.
func launchQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// launchPlist is the job's property list.
func launchPlist(label, script string) []byte {
	esc := func(v string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(v))
		return b.String()
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + esc(label) + `</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>` + esc(script) + `</string></array>
<key>LimitLoadToSessionType</key><string>Background</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><false/>
<key>StandardOutPath</key><string>/dev/null</string>
<key>StandardErrorPath</key><string>/dev/null</string>
</dict></plist>
`)
}

// launchLabel names the job: the loop's state directory, hashed, and a
// random suffix so a job left by an earlier start never collides.
func launchLabel(goos, stateDir string) string {
	sum := sha256.Sum256([]byte(stateDir))
	var r [3]byte
	_, _ = rand.Read(r[:])
	if goos == "linux" {
		return "looptender-" + hex.EncodeToString(sum[:6]) + "-" + hex.EncodeToString(r[:])
	}
	return "dev.looptender.loop." + hex.EncodeToString(sum[:6]) + "." + hex.EncodeToString(r[:])
}

// startLaunched starts the daemon through the service manager. ok is false
// when the manager refused before anything started, so the caller falls
// back; once the job is loaded, the result is final.
func startLaunched(e env, executable, path string, cfg *config.Config, allowShare bool) (code int, ok bool) {
	stateDir := cfg.Loop.StateDir
	if os.Getenv("BB_THREAD_ID") == "" || os.Getenv(backgroundLaunchEnv) == "child" {
		return 0, false
	}
	dir, err := os.MkdirTemp(stateDir, ".launch-")
	if err != nil {
		return 0, false
	}
	defer os.RemoveAll(dir)
	envFile, ackFile := filepath.Join(dir, "env.json"), filepath.Join(dir, "ack.json")
	label := launchLabel(runtime.GOOS, stateDir)
	child := []string{executable, "__start-background", "--launch-label", label, "--env-file", envFile, "--ack-file", ackFile, "-c", path}
	if allowShare {
		child = append(child, "--allow-second-loop")
	}
	if runtime.GOOS == "darwin" {
		clearStaleLaunches(stateDir, os.Getuid())
	}
	plan, found := backgroundLaunch(runtime.GOOS, exec.LookPath, label, child, os.Getuid(), stateDir)
	if !found {
		return 0, false
	}
	for path, b := range plan.Files {
		if os.WriteFile(path, b, 0o600) != nil {
			removeLaunchFiles(plan)
			return 0, false
		}
	}
	// The daemon's environment goes in a private file it reads and removes,
	// never in argv. Only what it needs is passed (launchEnvironment): the
	// thread's tokens and other secrets stay behind.
	vars, dropped := launchEnvironment(os.Environ(), reportEnvNames(cfg))
	b, _ := json.Marshal(vars)
	if os.WriteFile(envFile, b, 0o600) != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, plan.Argv[0], plan.Argv[1:]...).CombinedOutput(); err != nil {
		removeLaunchFiles(plan)
		e.errf("start: warning: %s did not take the daemon (%s)", plan.Kind, strings.TrimSpace(clip(string(out), 200)))
		return 0, false
	}
	var reply startReply
	for {
		if b, err := os.ReadFile(ackFile); err == nil && json.Unmarshal(b, &reply) == nil {
			break
		}
		if ctx.Err() != nil {
			removeLaunched(plan)
			e.errf("start: the daemon %s started did not acknowledge startup; its job was removed; inspect status before retrying", plan.Kind)
			return exitFailed, true
		}
		time.Sleep(25 * time.Millisecond)
	}
	if reply.Code != exitOK {
		e.errf("%s", reply.Message)
		return reply.Code, true
	}
	if reply.PID <= 0 || reply.Control == "" {
		removeLaunched(plan)
		e.errf("start: invalid startup acknowledgement; its job was removed")
		return exitFailed, true
	}
	if len(dropped) > 0 {
		// Names only, never values.
		e.errf("start: warning: the daemon does not get %s from this environment: they are named like credentials and this loop's report does not name them. Give a report its secret with secret_file in the configuration.", strings.Join(dropped, ", "))
	}
	if plan.Kind == "systemd" && !lingerOn() {
		e.errf("start: warning: lingering is off for this user, so the user's systemd, and this daemon with it, stops at logout. Run loginctl enable-linger, or looptender service install.")
	}
	return reportStartedBy(e, reply, plan), true
}

// reportEnvNames are the environment variables this loop's own report
// reads: its url_env, secret_env and user_env, defaults included, with the
// earlier KEEPGOING_ name of each looptender default, which reports also
// read. A launched daemon keeps exactly these of the credential-like names.
func reportEnvNames(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	r := cfg.Loop.Report
	if r == nil {
		return out
	}
	for _, n := range []string{r.URLEnv, r.SecretEnv, r.UserEnv} {
		if n == "" {
			continue
		}
		out[n] = true
		if rest, ok := strings.CutPrefix(n, envvar.Prefix); ok {
			out[envvar.LegacyPrefix+rest] = true
		}
	}
	return out
}

// lingerOn says whether the user's systemd keeps running after logout.
// Unknown counts as on: no warning is given on a guess.
func lingerOn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u := os.Getenv("USER")
	if u == "" {
		return true
	}
	out, err := exec.CommandContext(ctx, "loginctl", "show-user", u, "-p", "Linger").Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != "Linger=no"
}

func removeLaunched(p launchPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch p.Kind {
	case "launchd":
		_ = exec.CommandContext(ctx, p.Argv[0], "bootout", p.Target).Run()
		removeLaunchFiles(p)
	case "systemd":
		if sc, err := exec.LookPath("systemctl"); err == nil {
			_ = exec.CommandContext(ctx, sc, "--user", "stop", p.Label+".service").Run()
		}
	}
}

func removeLaunchFiles(p launchPlan) {
	for path := range p.Files {
		_ = os.Remove(path)
	}
}

// launchedChildArgs takes the launch flags off a __start-background command
// line, loads the environment the starting command passed, and returns where
// to acknowledge startup. A job started without its environment file (a
// service manager starting it again) does not run.
func launchedChildArgs(args []string) (rest []string, ackFile string, err error) {
	var label, envFile string
	for len(args) >= 2 {
		switch args[0] {
		case "--launch-label":
			label = args[1]
		case "--env-file":
			envFile = args[1]
		case "--ack-file":
			ackFile = args[1]
		default:
			return args, ackFile, finishLaunchEnv(label, envFile)
		}
		args = args[2:]
	}
	return args, ackFile, finishLaunchEnv(label, envFile)
}

func finishLaunchEnv(label, envFile string) error {
	if envFile == "" {
		return nil
	}
	b, err := os.ReadFile(envFile)
	_ = os.Remove(envFile)
	var vars []string
	if err == nil {
		err = json.Unmarshal(b, &vars)
	}
	if err != nil {
		// The job's wrapper removes the job when this exits.
		return errors.New("start: the launch environment is gone; this job was started again by its service manager and does not run")
	}
	applyLaunchEnv(vars)
	if label != "" {
		os.Setenv(launchLabelEnv, label)
	}
	return nil
}

// applyLaunchEnv replaces this process's environment; tests replace it so
// the test process keeps its own.
var applyLaunchEnv = func(vars []string) {
	os.Clearenv()
	for _, kv := range vars {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			os.Setenv(k, v)
		}
	}
}

// writeAck publishes the startup reply where the starting command waits.
func writeAck(path string, r startReply) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func reportStartedBy(e env, reply startReply, plan launchPlan) int {
	if reply.ReferenceUnavailable {
		e.errf("%s", registryPublicationWarning)
	}
	if _, asJSON := e.stdout.(*startJSONWriter); asJSON {
		_ = json.NewEncoder(e.stdout).Encode(startJSON{SchemaVersion: 1, Outcome: "started", PID: reply.PID, Control: reply.Control, ReferenceUnavailable: reply.ReferenceUnavailable, Launch: plan.Kind, Job: plan.Label})
		return exitOK
	}
	stop := launchStopCommand(plan.Kind, plan.Label, plan.Target)
	fmt.Fprintf(e.stdout, "started background daemon (PID %d) as a %s job, so it keeps running when BB quits; control: %s\nUse status to inspect, pause to hold, or stop to stop the agent. End the daemon with %s or by terminating its PID. No service was installed.\n", reply.PID, plan.Kind, reply.Control, stop)
	return exitOK
}

// childLaunchWarning is said when a daemon started inside a BB thread runs
// as that thread's child.
func childLaunchWarning(e env) {
	if os.Getenv("BB_THREAD_ID") == "" || os.Getenv(backgroundLaunchEnv) == "child" {
		return
	}
	if runtime.GOOS == "windows" {
		e.errf("start: warning: started inside a BB thread; on Windows it is not known whether this daemon keeps running when BB quits. Start it from a terminal outside BB to be sure.")
		return
	}
	e.errf("start: warning: started inside a BB thread as its child, so it stops when BB quits. To keep supervision across BB restarts, run looptender service install.")
}

// launchEnvironment is what a launched daemon gets of the starting
// command's environment: the variables it needs to find its programs, its
// files and the provider directories, looptender's own settings, and the
// variables its own report names (report). No other variable named like a
// credential is passed; dropped lists those of looptender's own (names
// only), so start can say so. Like a service, it carries nothing else.
func launchEnvironment(env []string, report map[string]bool) (out, dropped []string) {
	keep := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TMPDIR": true, "TZ": true, "LANG": true,
		"XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true,
		"CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true}
	out = []string{}
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		ours := strings.HasPrefix(k, envvar.Prefix) || strings.HasPrefix(k, envvar.LegacyPrefix)
		switch {
		case keep[k] || strings.HasPrefix(k, "LC_") || report[k]:
			out = append(out, kv)
		case ours && !launchSecretName(k):
			out = append(out, kv)
		case ours:
			dropped = append(dropped, k)
		}
	}
	return out, dropped
}

// launchSecretName: a variable named like a credential.
func launchSecretName(k string) bool {
	for _, w := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "PASS", "WEBHOOK", "AUTH", "CREDENTIAL", "COOKIE"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

type launchJob struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Target is the launchd service target, user/<uid>/<label>.
	Target string    `json:"target,omitempty"`
	Since  time.Time `json:"since"`
}

// recordLaunchJob writes the daemon's launch-job record, when it runs in
// one, and returns what removes it as the daemon ends.
func recordLaunchJob(stateDir string) func() {
	label := os.Getenv(launchLabelEnv)
	if label == "" {
		return func() {}
	}
	j := launchJob{Kind: "launchd", Label: label, Target: fmt.Sprintf("user/%d/%s", os.Getuid(), label), Since: time.Now().UTC()}
	if runtime.GOOS == "linux" {
		j.Kind, j.Target = "systemd", ""
	}
	b, _ := json.Marshal(j)
	path := filepath.Join(stateDir, launchJobName)
	if os.WriteFile(path, append(b, '\n'), 0o600) != nil {
		return func() {}
	}
	return func() { _ = os.Remove(path) }
}

// readLaunchJob is the daemon's launch job, for status and doctor; nil when
// it runs in none, or when its job is not running: the service manager no
// longer has it, or has it with no process (a daemon or its job script
// killed outright leaves the record, and on macOS the job, behind).
func readLaunchJob(stateDir string) *launchJob {
	j := readLaunchJobRecord(stateDir)
	if j == nil || !launchJobRunning(*j) {
		return nil
	}
	return j
}

// launchctlPrint is launchctl print of a service target: its output, and
// whether launchd has the job at all. It is a stand-in in tests.
var launchctlPrint = func(target string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p, err := exec.LookPath("launchctl")
	if err != nil {
		return "", false
	}
	out, err := exec.CommandContext(ctx, p, "print", target).Output()
	return string(out), err == nil
}

var launchdPIDLine = regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)\s*$`)

// launchdPID is the job's process in launchctl print's output; 0 when the
// job has none.
func launchdPID(out string) int {
	m := launchdPIDLine.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// launchJobRunning asks the service manager whether the job is there and
// has a process.
var launchJobRunning = func(j launchJob) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch j.Kind {
	case "launchd":
		if j.Target == "" {
			return false
		}
		out, ok := launchctlPrint(j.Target)
		return ok && launchdPID(out) > 0
	case "systemd":
		p, err := exec.LookPath("systemctl")
		return err == nil && exec.CommandContext(ctx, p, "--user", "is-active", "--quiet", j.Label+".service").Run() == nil
	}
	return false
}

func readLaunchJobRecord(stateDir string) *launchJob {
	b, err := os.ReadFile(filepath.Join(stateDir, launchJobName))
	if err != nil || len(b) > 4096 {
		return nil
	}
	var j launchJob
	if json.Unmarshal(b, &j) != nil || j.Label == "" {
		return nil
	}
	return &j
}

func launchStopCommand(kind, label, target string) string {
	if kind == "systemd" {
		return "systemctl --user stop " + label + ".service"
	}
	return "launchctl bootout " + target
}

// clearStaleLaunches removes what an earlier launch left when its job
// script was killed before its last lines (SIGKILL, power loss): the
// property list and script in the state directory, the job registered in
// launchd with no process, and the launch-job record. A job that still
// has a process is left alone: its daemon holds the loop's lock, and the
// new daemon is turned away by it.
func clearStaleLaunches(stateDir string, uid int) {
	plists, _ := filepath.Glob(filepath.Join(stateDir, "launch-dev.looptender.loop.*.plist"))
	scripts, _ := filepath.Glob(filepath.Join(stateDir, "launch-dev.looptender.loop.*.sh"))
	labels := map[string]bool{}
	for _, f := range append(plists, scripts...) {
		name := strings.TrimPrefix(filepath.Base(f), "launch-")
		labels[strings.TrimSuffix(strings.TrimSuffix(name, ".plist"), ".sh")] = true
	}
	if j := readLaunchJobRecord(stateDir); j != nil && j.Kind == "launchd" {
		labels[j.Label] = true
	}
	for label := range labels {
		target := fmt.Sprintf("user/%d/%s", uid, label)
		out, registered := launchctlPrint(target)
		if registered && launchdPID(out) > 0 {
			continue
		}
		if registered {
			launchctlBootout(target)
		}
		_ = os.Remove(filepath.Join(stateDir, "launch-"+label+".plist"))
		_ = os.Remove(filepath.Join(stateDir, "launch-"+label+".sh"))
		if j := readLaunchJobRecord(stateDir); j != nil && j.Label == label {
			_ = os.Remove(filepath.Join(stateDir, launchJobName))
		}
	}
}

// launchctlBootout boots a job out; a stand-in in tests.
var launchctlBootout = func(target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if p, err := exec.LookPath("launchctl"); err == nil {
		_ = exec.CommandContext(ctx, p, "bootout", target).Run()
	}
}
