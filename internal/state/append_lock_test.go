package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/snapshot"
)

// An event written while another appender holds the log's lock too long is
// written all the same, and says the lock was not obtained.
func TestEventWrittenWithoutTheLockSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Event{Loop: "example-loop", Event: "daemon", Reason: "started"}); err != nil {
		t.Fatal(err)
	}
	release, err := snapshot.HoldAppendLockForTest(filepath.Join(dir, LogName))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	saved := snapshot.AppendLockWait
	snapshot.AppendLockWait = 100 * time.Millisecond
	defer func() { snapshot.AppendLockWait = saved }()
	if err := Append(dir, Event{Loop: "example-loop", Event: "tick", Extra: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, LogName))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "appendLock") || !strings.Contains(lines[1], `"appendLock":"not_obtained"`) || !strings.Contains(lines[1], `"k":"v"`) {
		t.Fatalf("%s", b)
	}
}
