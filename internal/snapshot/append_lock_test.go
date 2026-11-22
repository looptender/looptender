package snapshot

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperHoldAppendLock is another process holding a log's appenders'
// lock until its standard input closes, as one suspended or stuck on a hung
// file system would.
func TestHelperHoldAppendLock(t *testing.T) {
	path := os.Getenv("SNAPSHOT_HOLD_APPEND_LOCK")
	if path == "" {
		return
	}
	release, err := HoldAppendLockForTest(path)
	if err != nil {
		os.Exit(3)
	}
	defer release()
	os.Stdout.WriteString("locked\n")
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

// An appender waits for a lock another process holds only so long, then
// appends without it, and says so to a caller that asks.
func TestAppendLockWaitIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := AppendLine(path, []byte(`{"n":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldAppendLock$")
	cmd.Env = append(os.Environ(), "SNAPSHOT_HOLD_APPEND_LOCK="+path)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper: %q", line)
	}
	saved := AppendLockWait
	AppendLockWait = 300 * time.Millisecond
	t.Cleanup(func() { AppendLockWait = saved })
	start := time.Now()
	var sawLocked *bool
	err := AppendLineNoting(path, func(locked bool) []byte { sawLocked = &locked; return []byte(`{"n":2}`) }, 0o600)
	waited := time.Since(start)
	if err != nil || sawLocked == nil || *sawLocked {
		t.Fatalf("err %v, locked %v", err, sawLocked)
	}
	if waited < 300*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("waited %s", waited)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"n\":1}\n{\"n\":2}\n" {
		t.Fatalf("%q", b)
	}
	// Released: the next append has the lock again.
	stdin.Close()
	cmd.Wait()
	if err := AppendLineNoting(path, func(locked bool) []byte { sawLocked = &locked; return []byte(`{"n":3}`) }, 0o600); err != nil || !*sawLocked {
		t.Fatalf("after release: %v %v", err, *sawLocked)
	}
}
