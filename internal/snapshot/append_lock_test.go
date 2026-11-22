package snapshot

import (
	"bufio"
	"errors"
	"fmt"
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

// holdFromAnotherProcess starts the helper holding the lock on path until
// the returned function is called.
func holdFromAnotherProcess(t *testing.T, path string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldAppendLock$")
	cmd.Env = append(os.Environ(), "SNAPSHOT_HOLD_APPEND_LOCK="+path)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release := func() { stdin.Close(); cmd.Wait() }
	t.Cleanup(release)
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper: %q", line)
	}
	return release
}

func shortLockWait(t *testing.T, wait, skip time.Duration) {
	t.Helper()
	savedWait, savedSkip := AppendLockWait, AppendLockSkip
	AppendLockWait, AppendLockSkip = wait, skip
	unobtainableMu.Lock()
	unobtainable = map[string]time.Time{}
	unobtainableMu.Unlock()
	t.Cleanup(func() { AppendLockWait, AppendLockSkip = savedWait, savedSkip })
}

// Without the lock a failed write is not cut back (a cut could remove
// another appender's line), and the next line starts on a line of its own.
func TestNoCutBackWithoutTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := AppendLine(path, []byte(`{"n":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	holdFromAnotherProcess(t, path)
	shortLockWait(t, 100*time.Millisecond, time.Minute)
	failing(t, "runs.jsonl", 4)
	if err := AppendLine(path, []byte(`{"n":2}`), 0o600); !errors.Is(err, ENOSPC) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"n\":1}\n{\"n\"" {
		t.Fatalf("cut back without the lock, or not written: %q", b)
	}
	InjectWriteFaults(nil)
	if err := AppendLine(path, []byte(`{"n":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"n\":1}\n{\"n\"\n{\"n\":3}\n" {
		t.Fatalf("the next line did not start on its own: %q", b)
	}
}

// With a holder stuck, only the first append of a while waits for the lock;
// the next ones try once and go on, until the while is over.
func TestAStuckHolderCostsOneWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := AppendLine(path, []byte(`{"n":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	release := holdFromAnotherProcess(t, path)
	shortLockWait(t, 400*time.Millisecond, 2*time.Second)
	timed := func(n int) time.Duration {
		start := time.Now()
		if err := AppendLine(path, []byte(fmt.Sprintf(`{"n":%d}`, n)), 0o600); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	if d := timed(1); d < 400*time.Millisecond {
		t.Fatalf("the first append did not wait: %s", d)
	}
	for i := 2; i < 6; i++ {
		if d := timed(i); d > 200*time.Millisecond {
			t.Fatalf("append %d waited again: %s", i, d)
		}
	}
	time.Sleep(2100 * time.Millisecond) // the while is over: wait again
	if d := timed(6); d < 400*time.Millisecond {
		t.Fatalf("waited no more after the while: %s", d)
	}
	// Once the lock can be had, it is taken at once.
	release()
	if d := timed(7); d > 200*time.Millisecond {
		t.Fatalf("a free lock was waited for: %s", d)
	}
	var sawLocked bool
	AppendLineNoting(path, func(locked bool) []byte { sawLocked = locked; return []byte(`{"n":8}`) }, 0o600)
	if !sawLocked {
		t.Fatal("the lock was not taken once free")
	}
}
