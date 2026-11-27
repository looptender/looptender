package lock

import (
	"errors"
	"strings"
	"testing"
)

type failedLockTable struct{}

func (failedLockTable) Read([]byte) (int, error) { return 0, errors.New("fixture read error") }

func TestInspectLinuxTable(t *testing.T) {
	for _, tc := range []struct{ name, table, state, reason string }{
		{"exclusive", "1: FLOCK ADVISORY WRITE 42 08:01:123 0 EOF\n", "held", "holder_reference_unavailable"},
		{"shared", "1: FLOCK ADVISORY READ 42 08:01:123 0 EOF\n", "held", "holder_reference_unavailable"},
		{"empty", "", "unavailable", "no_visible_lock"},
		{"other inode", "1: FLOCK ADVISORY WRITE 42 08:01:124 0 EOF\n", "unavailable", "no_visible_lock"},
		{"other device", "1: FLOCK ADVISORY WRITE 42 09:01:123 0 EOF\n", "unavailable", "no_visible_lock"},
		{"waiter", "1: -> FLOCK ADVISORY WRITE 42 08:01:123 0 EOF\n", "unavailable", "no_visible_lock"},
		{"record lock", "1: POSIX ADVISORY WRITE 42 08:01:123 0 EOF\n", "unavailable", "lock_query_unknown"},
		{"bad identity", "1: FLOCK ADVISORY WRITE 42 private 0 EOF\n", "unavailable", "lock_query_unknown"},
		{"partial", "1: FLOCK\n", "unavailable", "lock_query_unknown"},
		{"truncated after match", "1: FLOCK ADVISORY WRITE 42 08:01:123 0 EOF\npartial\n", "unavailable", "lock_query_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, reason := inspectLinuxTable(strings.NewReader(tc.table), 8, 1, 123)
			if state != tc.state || reason != tc.reason {
				t.Fatalf("%s %s", state, reason)
			}
		})
	}
	state, reason := inspectLinuxTable(failedLockTable{}, 8, 1, 123)
	if state != "unavailable" || reason != "lock_query_failed" {
		t.Fatalf("%s %s", state, reason)
	}
	// Even repeated valid lines cannot make a truncated table authoritative.
	table := strings.Repeat("1: FLOCK ADVISORY WRITE 42 08:01:123 0 EOF\n", 30000)
	state, reason = inspectLinuxTable(strings.NewReader(table), 8, 1, 123)
	if state != "unavailable" || reason != "lock_query_failed" {
		t.Fatalf("%s %s", state, reason)
	}
}
