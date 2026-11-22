package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/testexec"
)

// Exercise the real configured process deadline through the built CLI. A TCP
// handshake proves status started; EOF proves that command no longer owns its
// connection. The helper has no descendants and a finite independent lifetime.
func TestScenarioProcessStatusTimeout(t *testing.T) {
	d, err := testexec.NewDir(filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := d.Build("example-process", "./processtimeouthelper", nil, "warm")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld(t, scenarioStart)
	limitLoop(w, strings.Replace(newThreadFallback, "new_thread: true,", "new_thread: true, project: prj_example, environment: env_example,", 1))
	dir := t.TempDir()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	argv := func(op string) []string { return []string{helper, dir, op, listener.Addr().String()} }
	w.EditConfig(func(l map[string]any) {
		l["adapter"] = "process"
		delete(l, "bb")
		l["process"] = map[string]any{"status_command": argv("status"), "resume_command": argv("resume"), "timeout": "3s"}
	})
	mode := func(value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, "calls"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tick := func() Result {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, keepgoingBin, "tick", "-c", w.Config, "--now", w.stamp())
		cmd.Dir, cmd.Env = w.Root, w.env
		cmd.WaitDelay = 5 * time.Second
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			code = -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
		}
		return Result{Code: code, Stdout: out.String(), Stderr: stderr.String()}
	}
	line := func(r Result) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
			t.Fatalf("tick JSON: %v: %+v", err, r)
		}
		return v
	}
	mode("block")
	w.Must("enable", "-c", w.Config)
	w.Advance(time.Minute)
	var beforeCalls [][]string
	readLines(t, filepath.Join(w.world, "calls.jsonl"), &beforeCalls)
	done := make(chan Result, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); done <- tick() }()
	// On any assertion failure, close the handshake, then wait for the exact
	// CLI we started. No PID scans, process groups or broad kill operations.
	defer func() {
		select {
		case <-finished:
		case <-time.After(50 * time.Second):
			t.Error("owned tick did not finish cleanup")
		}
	}()
	listener.SetDeadline(time.Now().Add(15 * time.Second))
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("status never reached its handshake: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var b [1]byte
	if n, err := conn.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("status helper did not terminate: n=%d err=%v", n, err)
	}
	r := <-done
	v := line(r)
	if r.Code != 0 || v["outcome"] != "failed" || v["state"] != "unknown" || v["action"] != "none" || v["handoff"] != nil || !strings.Contains(r.Stderr, "timed out after 3s") {
		t.Fatalf("deadline must remain unknown without action: result=%+v JSON=%v", r, v)
	}
	if fileExists(filepath.Join(dir, "escaped")) {
		t.Fatal("helper escaped its block instead of being terminated by the configured deadline")
	}
	if got := calls(); got != "status\n" {
		t.Fatalf("timeout invoked extra process commands: %q", got)
	}
	var afterCalls [][]string
	readLines(t, filepath.Join(w.world, "calls.jsonl"), &afterCalls)
	if len(afterCalls) != len(beforeCalls) {
		t.Fatalf("unknown status invoked BB: %v", afterCalls[len(beforeCalls):])
	}
	var sent []Send
	readLines(t, filepath.Join(w.world, "sends.jsonl"), &sent)
	if len(sent) != 0 {
		t.Fatalf("timeout sent or created a fallback: %+v", sent)
	}
	status := w.Must("status", "-c", w.Config, "--json")
	var st Status
	if err := json.Unmarshal([]byte(status.Stdout), &st); err != nil {
		t.Fatal(err)
	}
	if st.Target != "primary" || st.NewThreads.Created != 0 || st.NewThreads.Pending {
		t.Fatalf("timeout changed target/creation state: %s", status.Stdout)
	}

	mode("idle")
	w.Advance(time.Minute)
	r = tick()
	v = line(r)
	if r.Code != 0 || v["action"] != "wake" || calls() != "status\nstatus\nresume\n" {
		t.Fatalf("healthy follow-up did not resume primary: %+v JSON=%v calls=%q", r, v, calls())
	}

	// Same setup, different structured observation: a real rate limit must
	// exercise the opted-in fallback, so a broken setup cannot pass suppression.
	mode("limit")
	w.Advance(10 * time.Minute)
	r = tick()
	v = line(r)
	readLines(t, filepath.Join(w.world, "sends.jsonl"), &sent)
	if r.Code != 0 || v["action"] != "handoff" || len(sent) != 1 || sent[0].Kind != "spawn" {
		t.Fatalf("rate-limit control did not create its configured fallback: %+v JSON=%v sends=%+v", r, v, sent)
	}
	if calls() != "status\nstatus\nresume\nstatus\n" {
		t.Fatalf("unexpected primary command: %q", calls())
	}
}
