//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
// that record their arguments and start the job detached and with an empty
// environment, as the service manager would: launchctl bootstrap runs the
// script its property list names, systemd-run the command after --. They
// keep a copy of the environment file the daemon is given, for the test to
// read. LAUNCH_FAIL=1 makes the launch fail.
func launchStandIns(t *testing.T, dir, record string) string {
	t.Helper()
	// The record's path is written in: the job, its script and its
	// launchctl bootout run with an empty environment.
	script := `#!/bin/sh
LAUNCH_RECORD=` + record + `
echo "$(basename "$0") $*" >> "$LAUNCH_RECORD"
case "$(basename "$0") $1" in
"launchctl bootstrap")
  [ "$LAUNCH_FAIL" = 1 ] && exit 1
  case "$2" in user/*) ;; *) exit 5;; esac
  grep -q '<key>LimitLoadToSessionType</key><string>Background</string>' "$3" || exit 5
  job=$(sed -n 's|.*<string>\(/[^<]*\.sh\)</string>.*|\1|p' "$3")
  env=$(sed -n "s/.*'--env-file' '\([^']*\)'.*/\1/p" "$job")
  cp "$env" "$LAUNCH_RECORD.env"
  /usr/bin/env -i /usr/bin/nohup /bin/sh "$job" </dev/null >/dev/null 2>&1 &
  echo $! > "$LAUNCH_RECORD.pid.$(basename "$3" .plist)"
  ;;
"launchctl print")
  # A job with a live process prints its pid, as launchd does.
  f="$LAUNCH_RECORD.pid.launch-${2##*/}"
  [ -f "$f" ] || exit 113
  pid=$(cat "$f")
  kill -0 "$pid" 2>/dev/null && printf '\tstate = running\n\tpid = %s\n' "$pid"
  exit 0
  ;;
"launchctl bootout")
  rm -f "$LAUNCH_RECORD.pid.launch-${2##*/}"
  ;;
"systemd-run --user")
  [ "$LAUNCH_FAIL" = 1 ] && exit 1
  while [ "$1" != "--" ]; do shift; done
  shift
  prev=
  for a in "$@"; do [ "$prev" = --env-file ] && cp "$a" "$LAUNCH_RECORD.env"; prev=$a; done
  /usr/bin/env -i /usr/bin/nohup "$@" </dev/null >/dev/null 2>&1 &
  ;;
esac
exit 0
`
	for _, name := range []string{"launchctl", "systemd-run", "systemctl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// No lingering: the user's systemd stops at logout.
	if err := os.WriteFile(filepath.Join(dir, "loginctl"), []byte("#!/bin/sh\necho Linger=no\n"), 0o700); err != nil {
		t.Fatal(err)
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
	// Whatever happens to the test, nothing it launched outlives it: every
	// process whose command line names this test's directory (the job
	// scripts and the daemons they run) is ended. Registered first, so it
	// runs last, after any other cleanup, even when the test fails.
	t.Cleanup(func() { endLaunchedUnder(t, dir) })
	tools, err := testexec.NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := tools.Build("looptender", ".", nil, "version")
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "launch-record")
	fake := launchStandIns(t, t.TempDir(), record)
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
  report: {format: ntfy}
`, quote("/bin/echo"))), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := control.Save(stateDir, control.File{Paused: true, Note: "fictional review"}); err != nil {
		t.Fatal(err)
	}
	start := func(extra ...string) (map[string]any, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "start", "--background", "--json", "-c", cfg)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "BB_THREAD_ID=thr_example_launch", backgroundLaunchEnv+"=", "PATH="+fake+":"+os.Getenv("PATH"), "LAUNCH_RECORD="+record,
			"EXAMPLE_API_TOKEN=fictional-secret", "LOOPTENDER_TELEGRAM_BOT_TOKEN=fictional-secret", "LOOPTENDER_NTFY_URL=https://ntfy.example.com/example", "LOOPTENDER_NTFY_TOKEN=fictional-report-token")
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

	kind, submit := "launchd", fmt.Sprintf("launchctl bootstrap user/%d %s/launch-dev.looptender.loop.", os.Getuid(), stateDir)
	if runtime.GOOS == "linux" {
		kind, submit = "systemd", "systemd-run --user --unit looptender-"
	}
	v, errs := start()
	job, _ := v["job"].(string)
	if v["launch"] != kind || job == "" || strings.Contains(errs, "stops when BB quits") {
		rec, _ := os.ReadFile(record)
		t.Fatalf("not handed to %s: %v %s; launcher record: %s", kind, v, errs, rec)
	}
	if lingers := strings.Contains(errs, "lingering is off"); lingers != (runtime.GOOS == "linux") {
		t.Fatalf("linger warning on %s: %s", runtime.GOOS, errs)
	}
	// The looptender variables it does not pass are named, never their values.
	if !strings.Contains(errs, "does not get LOOPTENDER_TELEGRAM_BOT_TOKEN from this environment") || !strings.Contains(errs, "secret_file") || strings.Contains(errs, "fictional") {
		t.Fatalf("dropped names: %s", errs)
	}
	// status says which job the daemon runs in.
	var st struct {
		LaunchJob *launchJob `json:"launchJob"`
	}
	for deadline := time.Now().Add(30 * time.Second); st.LaunchJob == nil && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		status := exec.Command(bin, "status", "--json", "-c", cfg)
		status.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH")) // the stand-in has the job
		out, _ := status.Output()
		_ = json.Unmarshal(out, &st)
	}
	if st.LaunchJob == nil || st.LaunchJob.Label != job || st.LaunchJob.Kind != kind {
		t.Fatalf("status: %+v", st.LaunchJob)
	}
	// The environment the daemon was given has what it needs and no token
	// or thread ID of the starting command.
	env, err := os.ReadFile(record + ".env")
	if err != nil || strings.Contains(string(env), "fictional-secret") || strings.Contains(string(env), "thr_example_launch") || !strings.Contains(string(env), `"PATH=`) ||
		!strings.Contains(string(env), `"LOOPTENDER_NTFY_TOKEN=fictional-report-token"`) {
		t.Fatalf("daemon environment: %v %s", err, env)
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
		bootout := fmt.Sprintf("launchctl bootout user/%d/%s", os.Getuid(), job)
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(string(b), bootout) && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			b, _ = os.ReadFile(record)
		}
		if !strings.Contains(string(b), bootout) {
			t.Fatalf("job not removed: %q", b)
		}
		if left, _ := filepath.Glob(filepath.Join(stateDir, "launch-dev.looptender.loop.*")); len(left) != 0 {
			t.Fatalf("job files left: %v", left)
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

// launchd's job script boots its job out and removes its files however the
// daemon ends, including when the launch path no longer exists.
func TestLaunchJobScriptRemovesItsJobWhateverTheProgramDoes(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	fake := launchStandIns(t, t.TempDir(), record)
	find := func(string) (string, error) { return filepath.Join(fake, "launchctl"), nil }
	for _, program := range []string{"/bin/false", filepath.Join(t.TempDir(), "pruned-build", "looptender")} {
		dir := t.TempDir()
		p, ok := backgroundLaunch("darwin", find, "dev.looptender.loop.example", []string{program, "__start-background"}, 501, dir)
		if !ok {
			t.Fatal("no plan")
		}
		var script string
		for path, b := range p.Files {
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(path, ".sh") {
				script = path
			}
		}
		if err := exec.Command("/bin/sh", script).Run(); err != nil {
			t.Fatalf("%s: %v", program, err)
		}
		if left, _ := filepath.Glob(filepath.Join(dir, "launch-*")); len(left) != 0 {
			t.Fatalf("%s: files left %v", program, left)
		}
	}
	b, _ := os.ReadFile(record)
	if strings.Count(string(b), "launchctl bootout user/501/dev.looptender.loop.example") != 2 {
		t.Fatalf("%q", b)
	}
}

// status shows a launch job only while it is running: launchd has it and
// it has a process. A record left by a daemon killed outright, or a job its
// script was killed in, is not shown.
func TestStatusShowsALaunchJobOnlyWhileItRuns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, launchJobName), []byte(`{"kind":"launchd","label":"dev.looptender.loop.example","target":"user/501/dev.looptender.loop.example","since":"2026-03-04T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := launchctlPrint
	t.Cleanup(func() { launchctlPrint = saved })
	for _, c := range []struct {
		out        string
		registered bool
		shown      bool
	}{
		{"gui/501/x = {\n\tstate = running\n\tpid = 4242\n}\n", true, true},
		{"user/501/x = {\n\tstate = not running\n}\n", true, false},
		{"", false, false},
	} {
		launchctlPrint = func(string) (string, bool) { return c.out, c.registered }
		if got := readLaunchJob(dir) != nil; got != c.shown {
			t.Fatalf("%q registered %v: shown %v", c.out, c.registered, got)
		}
	}
	if launchdPID("\tpid = 12\n") != 12 || launchdPID("\tlast exit code = 0\n") != 0 {
		t.Fatal("pid parse")
	}
}

// staleLaunchDir is a state directory with launch files of this loop's own
// labels, old enough to be stale, and stand-ins for launchctl print and
// bootout: print answers from outputs by label suffix.
func staleLaunchDir(t *testing.T, outputs map[string]string, suffixes ...string) (dir string, labels map[string]string, booted *[]string) {
	t.Helper()
	dir = t.TempDir()
	sum := sha256.Sum256([]byte(dir))
	labels = map[string]string{}
	old := time.Now().Add(-time.Hour)
	for i, suffix := range suffixes {
		label := fmt.Sprintf("dev.looptender.loop.%s.%06x", hex.EncodeToString(sum[:6]), i+1)
		labels[suffix] = label
		for _, ext := range []string{".plist", ".sh"} {
			p := filepath.Join(dir, "launch-"+label+ext)
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	savedPrint, savedBootout := launchctlPrint, launchctlBootout
	t.Cleanup(func() { launchctlPrint, launchctlBootout = savedPrint, savedBootout })
	launchctlPrint = func(target string) (string, bool) {
		for suffix, label := range labels {
			if strings.HasSuffix(target, "/"+label) {
				out, ok := outputs[suffix]
				return out, ok
			}
		}
		return "", false
	}
	var b []string
	booted = &b
	launchctlBootout = func(target string) { b = append(b, target) }
	return dir, labels, booted
}

func launchFilesOf(dir, label string) int {
	n := 0
	for _, ext := range []string{".plist", ".sh"} {
		if _, err := os.Lstat(filepath.Join(dir, "launch-"+label+ext)); err == nil {
			n++
		}
	}
	return n
}

// A start first clears what an earlier launch left when its job script was
// killed before its last lines: its job, booted out only when launchctl
// positively says it is not running, its files and its record. A running
// job, or one whose state cannot be read, is left alone.
func TestStartClearsStaleLaunches(t *testing.T) {
	dir, labels, booted := staleLaunchDir(t, map[string]string{
		"stale":      "\tstate = not running\n",
		"live":       "\tstate = running\n\tpid = 4242\n",
		"unreadable": "some other wording entirely\n",
	}, "stale", "live", "unreadable", "gone")
	if err := os.WriteFile(filepath.Join(dir, launchJobName), []byte(`{"kind":"launchd","label":"`+labels["stale"]+`","since":"2026-03-04T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	clearStaleLaunches(dir, 501, time.Now())
	if len(*booted) != 1 || *booted != nil && (*booted)[0] != "user/501/"+labels["stale"] {
		t.Fatalf("booted out %v", *booted)
	}
	for suffix, want := range map[string]int{"stale": 0, "live": 2, "unreadable": 2, "gone": 0} {
		if got := launchFilesOf(dir, labels[suffix]); got != want {
			t.Fatalf("%s: %d files left, want %d", suffix, got, want)
		}
	}
	if readLaunchJobRecord(dir) != nil {
		t.Fatal("stale launch-job record kept")
	}
}

// Nothing is touched while the loop's daemon lock is held, whatever
// launchctl says, nor files young enough to belong to a start still
// waiting for its acknowledgement.
func TestStaleLaunchClearingWaitsForALiveOrStartingDaemon(t *testing.T) {
	dir, labels, booted := staleLaunchDir(t, map[string]string{"stale": "\tstate = not running\n"}, "stale")
	l, err := lock.AcquireNamed(dir, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	clearStaleLaunches(dir, 501, time.Now())
	l.Release()
	if len(*booted) != 0 || launchFilesOf(dir, labels["stale"]) != 2 {
		t.Fatalf("cleared under a held daemon lock: %v", *booted)
	}
	// Files from a launch a moment ago are a start still in progress.
	clearStaleLaunches(dir, 501, time.Now().Add(-time.Hour).Add(launchAckWait/2))
	if len(*booted) != 0 || launchFilesOf(dir, labels["stale"]) != 2 {
		t.Fatalf("cleared a launch still waiting for its acknowledgement: %v", *booted)
	}
	clearStaleLaunches(dir, 501, time.Now())
	if len(*booted) != 1 || launchFilesOf(dir, labels["stale"]) != 0 {
		t.Fatalf("not cleared once stale: %v", *booted)
	}
}

// A label not of this loop's own shape is never used: not a traversal from
// a forged record, not another loop's hash, so nothing outside the state
// directory is removed and no other job is booted out.
func TestStaleLaunchClearingTakesOnlyThisLoopsLabels(t *testing.T) {
	dir, _, booted := staleLaunchDir(t, map[string]string{}, "none")
	outside := filepath.Join(filepath.Dir(dir), "org.keepgoing.loop.example.plist")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "launch-dev.looptender.loop.000000000000.000001.plist")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(other, old, old)
	for _, label := range []string{"../org.keepgoing.loop.example", "dev.looptender.loop.000000000000.000001"} {
		if err := os.WriteFile(filepath.Join(dir, launchJobName), []byte(`{"kind":"launchd","label":"`+label+`","since":"2026-03-04T12:00:00Z"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		clearStaleLaunches(dir, 501, time.Now())
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a file outside the state directory was removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("another loop's launch file was removed")
	}
	if len(*booted) != 0 {
		t.Fatalf("booted out %v", *booted)
	}
	if !launchJobLabelFor(dir, launchLabel("darwin", dir)) || launchJobLabelFor(dir, "dev.looptender.loop.000000000000.000001") {
		t.Fatal("label check")
	}
}

// endLaunchedUnder ends every process whose command line names dir: what a
// test launched through the stand-in service managers. SIGTERM first, so a
// daemon writes its records and its job script runs its last lines, then
// SIGKILL for anything still there.
func endLaunchedUnder(t *testing.T, dir string) {
	t.Helper()
	find := func() []int {
		out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
		if err != nil {
			t.Logf("cleanup: ps: %v", err)
			return nil
		}
		var pids []int
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.Contains(line, dir) {
				continue
			}
			if pid, err := strconv.Atoi(f[0]); err == nil && pid != os.Getpid() {
				pids = append(pids, pid)
			}
		}
		return pids
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		pids := find()
		if len(pids) == 0 {
			return
		}
		if sig == syscall.SIGKILL {
			t.Logf("cleanup: killing %v, still running after SIGTERM", pids)
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && len(find()) > 0; time.Sleep(50 * time.Millisecond) {
		}
	}
	if left := find(); len(left) > 0 {
		t.Errorf("cleanup: processes from this test still running: %v", left)
	}
}

// The cleanup ends what a test launched, even a process that ignores
// SIGTERM, and leaves everything else alone.
func TestEndLaunchedUnderEndsOnlyTheTestsProcesses(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "stubborn.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer other.Process.Kill()
	stubborn := exec.Command("/bin/sh", script)
	if err := stubborn.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- stubborn.Wait() }()
	endLaunchedUnder(t, dir)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the test's process was not ended")
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("another process was ended: %v", err)
	}
}
