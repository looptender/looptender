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

// launchLabelEnv carries a launchd job's label to the daemon and to any
// build it adopts, so whichever process ends the daemon removes the job:
// launch submit keeps a job alive, and an ended daemon must stay ended.
const launchLabelEnv = "LOOPTENDER_LAUNCHD_LABEL"

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
		return launchPlan{Kind: "launchd", Label: label, Argv: append([]string{p, "submit", "-l", label, "--"}, child...)}, true
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
	// The environment goes in a private file the daemon reads and removes,
	// never in argv: it can hold secrets, and the service manager would
	// otherwise start the daemon with its own environment.
	b, _ := json.Marshal(adoptCommandEnv(os.Environ()))
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

// releaseLaunchJob removes the launchd job this daemon runs in, if any, so
// launchd does not start it again once it has ended. Called as the daemon
// ends, after its last record.
func releaseLaunchJob() {
	label := os.Getenv(launchLabelEnv)
	if label == "" || runtime.GOOS != "darwin" {
		return
	}
	if p, err := exec.LookPath("launchctl"); err == nil {
		removeLaunched(launchPlan{Kind: "launchd", Label: label, Argv: []string{p}})
	}
}

// launchedChildArgs takes the launch flags off a __start-background command
// line, loads the environment the starting command passed, and returns where
// to acknowledge startup. A job started without its environment file (a
// service manager starting it again) removes itself and does not run.
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
		if label != "" {
			os.Setenv(launchLabelEnv, label)
			releaseLaunchJob()
		}
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
	stop := "launchctl remove " + plan.Label
	if plan.Kind == "systemd" {
		stop = "systemctl --user stop " + plan.Label + ".service"
	}
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
