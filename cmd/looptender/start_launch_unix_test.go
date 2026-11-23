//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/testexec"
)

// launchStandIns writes stand-ins for launchctl, systemd-run and systemctl
// that record their arguments and start the submitted command detached, as
// the service manager would. LAUNCH_FAIL=1 makes a submission fail.
func launchStandIns(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
echo "$(basename "$0") $*" >> "$LAUNCH_RECORD"
case "$1" in
submit|--user)
  [ "$LAUNCH_FAIL" = 1 ] && exit 1
  while [ "$1" != "--" ]; do shift; done
  shift
  nohup "$@" </dev/null >/dev/null 2>&1 &
  ;;
esac
exit 0
`
	for _, name := range []string{"launchctl", "systemd-run", "systemctl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Started inside a BB thread, a background daemon is handed to the
// platform's service manager (stand-ins here) as a one-off job, so it is no
// longer BB's child; its stopped record says which signal ended it, and on
// macOS it removes its own job so launchd does not start it again. When the
// manager refuses, it starts as before, with a warning.
func TestStartBackgroundInABBThreadOutlivesBB(t *testing.T) {
	dir := t.TempDir()
	tools, err := testexec.NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := tools.Build("looptender", ".", nil, "version")
	if err != nil {
		t.Fatal(err)
	}
	fake := launchStandIns(t, t.TempDir())
	record := filepath.Join(dir, "launch-record")
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	cfg := filepath.Join(dir, "looptender.yaml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`version: 1
loop:
  name: example-launch
  state_dir: state
  adapter: process
  process:
    status_command: [%[1]s, '{"state":"active"}']
    resume_command: [%[1]s, resume]
    stop_command: [%[1]s, stop]
  prompt: Continue the fictional example.
  daemon: {interval: 1h, control_poll: 100ms}
`, quote("/bin/echo"))), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := control.Save(stateDir, control.File{Paused: true, Note: "fictional review"}); err != nil {
		t.Fatal(err)
	}
	start := func(extra ...string) (map[string]any, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "start", "--background", "--json", "-c", cfg)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "BB_THREAD_ID=thr_example_launch", backgroundLaunchEnv+"=", "PATH="+fake+":"+os.Getenv("PATH"), "LAUNCH_RECORD="+record,
			"EXAMPLE_API_TOKEN=fictional-secret")
		cmd.Env = append(cmd.Env, extra...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("start: %v %s %s", err, stdout.String(), stderr.String())
		}
		var v map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &v); err != nil || v["outcome"] != "started" {
			t.Fatalf("start printed %q %s", stdout.String(), stderr.String())
		}
		return v, stderr.String()
	}
	end := func(v map[string]any) {
		t.Helper()
		pid := int(v["pid"].(float64))
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			if l, err := lock.AcquireNamed(stateDir, lock.Daemon); err == nil {
				l.Release()
				return
			}
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("daemon did not end on SIGTERM")
	}
	lastDaemonEvent := func() state.Event {
		t.Helper()
		f, err := os.Open(filepath.Join(stateDir, state.LogName))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		var last state.Event
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e state.Event
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event == "daemon" {
				last = e
			}
		}
		return last
	}

	kind, submit := "launchd", "launchctl submit -l dev.looptender.loop."
	if runtime.GOOS == "linux" {
		kind, submit = "systemd", "systemd-run --user --unit looptender-"
	}
	v, errs := start()
	job, _ := v["job"].(string)
	if v["launch"] != kind || job == "" || strings.Contains(errs, "stops when BB quits") {
		t.Fatalf("not handed to %s: %v %s", kind, v, errs)
	}
	// status says which job the daemon runs in.
	var st struct {
		LaunchJob *launchJob `json:"launchJob"`
	}
	for deadline := time.Now().Add(5 * time.Second); st.LaunchJob == nil && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		out, _ := exec.Command(bin, "status", "--json", "-c", cfg).Output()
		_ = json.Unmarshal(out, &st)
	}
	if st.LaunchJob == nil || st.LaunchJob.Label != job || st.LaunchJob.Kind != kind {
		t.Fatalf("status: %+v", st.LaunchJob)
	}
	if runtime.GOOS == "linux" {
		// The daemon's environment has no token from the starting command.
		env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", int(v["pid"].(float64))))
		if err != nil || strings.Contains(string(env), "fictional-secret") || strings.Contains(string(env), "thr_example_launch") {
			t.Fatalf("daemon environment: %v %q", err, env)
		}
	}
	end(v)
	if readLaunchJob(stateDir) != nil {
		t.Fatal("launch-job record left after the daemon ended")
	}
	b, _ := os.ReadFile(record)
	if !strings.Contains(string(b), submit) || strings.Contains(string(b), "thr_example_launch") {
		t.Fatalf("launch record %q", b)
	}
	if runtime.GOOS == "darwin" {
		// The job is removed once its daemon ended, after the record.
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(string(b), "launchctl remove "+job) && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			b, _ = os.ReadFile(record)
		}
		if !strings.Contains(string(b), "launchctl remove "+job) {
			t.Fatalf("job not removed: %q", b)
		}
	}
	if e := lastDaemonEvent(); e.Reason != "stopped" || e.Extra["signal"] != "SIGTERM" {
		t.Fatalf("stopped record %+v", e)
	}
	if entries, _ := filepath.Glob(filepath.Join(stateDir, ".launch-*")); len(entries) != 0 {
		t.Fatalf("launch files left behind: %v", entries)
	}

	// The service manager refuses: started as BB's child, with the warning.
	v, errs = start("LAUNCH_FAIL=1")
	if v["launch"] != nil || !strings.Contains(errs, "did not take the daemon") || !strings.Contains(errs, "stops when BB quits") || !strings.Contains(errs, "looptender service install") {
		t.Fatalf("fallback: %v %s", v, errs)
	}
	end(v)
}

// launchd's job wrapper removes its job however the program ends, including
// when the launch path no longer exists, so launchd never retries it.
func TestLaunchWrapperRemovesItsJobWhateverTheProgramDoes(t *testing.T) {
	fake := launchStandIns(t, t.TempDir())
	record := filepath.Join(t.TempDir(), "record")
	for _, program := range [][]string{{"/bin/false"}, {filepath.Join(t.TempDir(), "pruned-build", "looptender")}} {
		cmd := exec.Command("/bin/sh", append([]string{"-c", launchWrapper, "looptender-launch", filepath.Join(fake, "launchctl"), "dev.looptender.loop.example"}, program...)...)
		cmd.Env = append(os.Environ(), "LAUNCH_RECORD="+record)
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v", program, err)
		}
	}
	b, _ := os.ReadFile(record)
	if strings.Count(string(b), "launchctl remove dev.looptender.loop.example") != 2 {
		t.Fatalf("%q", b)
	}
}
