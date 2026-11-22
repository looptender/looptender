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

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/testexec"
)

func TestScenarioProcessResumeTimeout(t *testing.T) {
	d, err := testexec.NewDir(filepath.Join(t.TempDir(), "bin"))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := d.Build("example-resume", "./processresumehelper", nil, "warm")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		sends   int
		retries int
	}{
		{"total send cap", 2, 9}, {"episode cap", 9, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				l["errors"] = map[string]any{"max_reprompts": tc.retries, "backoff": "10m", "failover_on": []string{"rate_limit"}}
				l["limits"] = map[string]any{"max_sends": tc.sends}
			})
			mode := func(value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			receipts := func() int {
				t.Helper()
				b, err := os.ReadFile(filepath.Join(dir, "receipts"))
				if err != nil {
					t.Fatal(err)
				}
				return len(strings.Fields(string(b)))
			}
			// Every tick is a fresh built CLI with no in-memory counters carried over.
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
			result := func(r Result, action, outcome string) {
				t.Helper()
				var v map[string]any
				if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil || r.Code != 0 || v["action"] != action || v["outcome"] != outcome {
					t.Fatalf("want %s/%s: %+v JSON=%v parse=%v", action, outcome, r, v, err)
				}
			}
			accounting := func(want int, at time.Time, outcome string) {
				t.Helper()
				used, err := state.LoadSends(w.runtimeDir())
				if err != nil || used != int64(want) {
					t.Fatalf("send accounting: used=%d want=%d err=%v", used, want, err)
				}
				st, err := state.Load(w.runtimeDir())
				if err != nil {
					t.Fatal(err)
				}
				if st.Reprompts != want || !st.LastRepromptAt.Equal(at) {
					t.Fatalf("attempt/backoff accounting: count=%d at=%v want=%d at=%v", st.Reprompts, st.LastRepromptAt, want, at)
				}
				if st.LastPromptSend == nil || st.LastPromptSend.Outcome != outcome {
					t.Fatalf("input receipt is not a confirmed send: %+v", st.LastPromptSend)
				}
				if receipts() != want {
					t.Fatalf("input delivered %d times, want %d", receipts(), want)
				}
			}
			mode("block")
			w.Must("enable", "-c", w.Config)
			w.Advance(time.Minute)
			first := w.Now
			done := make(chan Result, 1)
			finished := make(chan struct{})
			go func() { defer close(finished); done <- tick() }()
			// Helper has no descendants. Close its connection on failure; wait for the
			// exact CLI (which waits/reaps its command), never search or signal by PID.
			defer func() {
				select {
				case <-finished:
				case <-time.After(50 * time.Second):
					t.Error("owned tick cleanup did not finish")
				}
			}()
			listener.SetDeadline(time.Now().Add(15 * time.Second))
			conn, err := listener.Accept()
			if err != nil {
				t.Fatalf("resume never acknowledged receipt: %v", err)
			}
			defer conn.Close()
			if receipts() != 1 {
				t.Fatal("handshake before receipt")
			}
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			var b [1]byte
			if n, err := conn.Read(b[:]); n != 0 || err != io.EOF {
				t.Fatalf("resume helper did not terminate: n=%d err=%v", n, err)
			}
			r := <-done
			result(r, "reprompt", "failed")
			if !strings.Contains(r.Stderr, "timed out after 3s") || fileExists(filepath.Join(dir, "escaped")) {
				t.Fatalf("not an adapter timeout: %+v", r)
			}
			accounting(1, first, "adapter_error")

			// Even after the command becomes healthy, a fresh check must respect the
			// backoff of the received-but-unconfirmed attempt, not deliver input again.
			mode("return")
			for i := 0; i < 2; i++ {
				w.Advance(time.Minute)
				r = tick()
				result(r, "none", "succeeded")
				if !strings.Contains(r.Stdout, "backing off after error") {
					t.Fatalf("backoff lost: %+v", r)
				}
				accounting(1, first, "adapter_error")
			}
			w.Advance(9 * time.Minute)
			second := w.Now
			r = tick()
			result(r, "reprompt", "succeeded")
			accounting(2, second, "adapter_returned")
			// Returning normally confirms only adapter acceptance, not task completion.
			w.Advance(11 * time.Minute)
			r = tick()
			result(r, "await_user", "succeeded")
			wantReason := "error persisted after re-prompts"
			if tc.sends == 2 {
				wantReason = "loop send limit reached"
			}
			if !strings.Contains(r.Stdout, wantReason) {
				t.Fatalf("wrong cap raised: want %q: %+v", wantReason, r)
			}
			accounting(2, second, "adapter_returned")
			ctl, err := control.Load(w.runtimeDir())
			if err != nil || !ctl.AwaitingUser || ctl.Completed {
				t.Fatalf("cap must hold without claiming completion: %+v %v", ctl, err)
			}
			w.Advance(time.Hour)
			r = tick()
			result(r, "none", "succeeded")
			accounting(2, second, "adapter_returned")
			var sent []Send
			readLines(t, filepath.Join(w.world, "sends.jsonl"), &sent)
			if len(sent) != 0 {
				t.Fatalf("unknown sends must not invoke fallback: %+v", sent)
			}
		})
	}
}
