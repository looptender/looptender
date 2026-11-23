package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A background daemon started inside a BB thread is a child of that
// thread's process, and quitting BB ends it. There it is handed to the
// user's own service manager instead, as a one-off job outside BB: launchd
// (launchctl submit) on macOS, the user's systemd (systemd-run --user) on
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

// launchWrapper is the program launchd runs for the job: the daemon, then,
// however it ended (stopped, crashed, refused, an adopted older build, a
// launch path that no longer exists), the removal of its own job.
// launchctl submit restarts a job's program whenever it exits, so without
// this an ended daemon would be started again, and a missing program
// retried for ever. $1 is launchctl, $2 the label, the rest the daemon.
const launchWrapper = `launchctl=$1; label=$2; shift 2; "$@"; exec "$launchctl" remove "$label"`

type launchPlan struct {
	Kind  string // launchd or systemd
	Label string
	Argv  []string
}

// backgroundLaunch is the service-manager launch for this platform, or
// false when there is none to use.
func backgroundLaunch(goos string, lookPath func(string) (string, error), label string, child []string) (launchPlan, bool) {
	switch goos {
	case "darwin":
		p, err := lookPath("launchctl")
		if err != nil {
			return launchPlan{}, false
		}
		return launchPlan{Kind: "launchd", Label: label, Argv: append([]string{p, "submit", "-l", label, "--", "/bin/sh", "-c", launchWrapper, "looptender-launch", p, label}, child...)}, true
	case "linux":
		p, err := lookPath("systemd-run")
		if err != nil {
			return launchPlan{}, false
		}
		return launchPlan{Kind: "systemd", Label: label, Argv: append([]string{p, "--user", "--unit", label, "--collect", "--quiet", "--"}, child...)}, true
	}
	return launchPlan{}, false
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
// back; once the job is submitted, the result is final.
func startLaunched(e env, executable, path, stateDir string, allowShare bool) (code int, ok bool) {
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
	plan, found := backgroundLaunch(runtime.GOOS, exec.LookPath, label, child)
	if !found {
		return 0, false
	}
	// The daemon's environment goes in a private file it reads and removes,
	// never in argv. Only what it needs is passed (launchEnvironment): the
	// thread's tokens and other secrets stay behind.
	b, _ := json.Marshal(launchEnvironment(os.Environ()))
	if os.WriteFile(envFile, b, 0o600) != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, plan.Argv[0], plan.Argv[1:]...).CombinedOutput(); err != nil {
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
	return reportStartedBy(e, reply, plan), true
}

func removeLaunched(p launchPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch p.Kind {
	case "launchd":
		_ = exec.CommandContext(ctx, p.Argv[0], "remove", p.Label).Run()
	case "systemd":
		if sc, err := exec.LookPath("systemctl"); err == nil {
			_ = exec.CommandContext(ctx, sc, "--user", "stop", p.Label+".service").Run()
		}
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
	stop := launchStopCommand(plan.Kind, plan.Label)
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
// files and the provider directories, and looptender's own settings, never
// a secret. Like a service (service install), it carries no other variable.
func launchEnvironment(env []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TMPDIR": true, "TZ": true, "LANG": true,
		"XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true,
		"CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true}
	out := []string{}
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		own := (strings.HasPrefix(k, "LOOPTENDER_") || strings.HasPrefix(k, "KEEPGOING_")) && !launchSecretName(k)
		if keep[k] || strings.HasPrefix(k, "LC_") || own {
			out = append(out, kv)
		}
	}
	return out
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
	Kind  string    `json:"kind"`
	Label string    `json:"label"`
	Since time.Time `json:"since"`
}

// recordLaunchJob writes the daemon's launch-job record, when it runs in
// one, and returns what removes it as the daemon ends.
func recordLaunchJob(stateDir string) func() {
	label := os.Getenv(launchLabelEnv)
	if label == "" {
		return func() {}
	}
	kind := "launchd"
	if runtime.GOOS == "linux" {
		kind = "systemd"
	}
	b, _ := json.Marshal(launchJob{Kind: kind, Label: label, Since: time.Now().UTC()})
	path := filepath.Join(stateDir, launchJobName)
	if os.WriteFile(path, append(b, '\n'), 0o600) != nil {
		return func() {}
	}
	return func() { _ = os.Remove(path) }
}

// readLaunchJob is the daemon's launch job, for status and doctor; nil when
// it runs in none.
func readLaunchJob(stateDir string) *launchJob {
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

func launchStopCommand(kind, label string) string {
	if kind == "systemd" {
		return "systemctl --user stop " + label + ".service"
	}
	return "launchctl remove " + label
}
