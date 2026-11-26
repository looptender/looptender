package rungraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectLocksDoesNotCreateGraphOrRuntime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "absent")
	locks := InspectLocks(dir)
	if len(locks) != 2 || locks[0].Name != "tick" || locks[1].Name != "daemon" {
		t.Fatalf("%+v", locks)
	}
	for _, l := range locks {
		if l.Availability != "none" {
			t.Fatalf("%+v", l)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("%v %v", entries, err)
	}
}

func TestInspectLocksIgnoresGraphAndDoesNotInventRunBindings(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"private":"untrusted fixture","currentRun":"run_fictional"}`)
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tick.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	locks := InspectLocks(dir)
	if locks[0].Reason != "lock_not_regular" || locks[1].Availability != "none" {
		t.Fatalf("%+v", locks)
	}
	got, err := os.ReadFile(filepath.Join(dir, "graph.json"))
	if err != nil || string(got) != string(raw) {
		t.Fatal("graph changed")
	}
}
