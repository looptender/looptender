package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pollFixture() UsagePoll {
	return UsagePoll{ID: strings.Repeat("a", 32), Revision: strings.Repeat("b", 32), Binding: strings.Repeat("c", 64), Executable: strings.Repeat("d", 64), Grant: strings.Repeat("e", 32), NextAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func TestUsagePollingStrictStorageAndAtomicBounds(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(dir)
	f, err := LoadUsagePolling(dir)
	after, _ := os.ReadDir(dir)
	if err != nil || len(f.Entries) != 0 || len(before) != len(after) {
		t.Fatal("missing settings wrote or enabled")
	}
	if err := UpdateUsagePolling(dir, func(f *UsagePolling) error { f.Entries = append(f.Entries, pollFixture()); return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, UsagePollingName)
	raw, _ := os.ReadFile(path)
	for _, bad := range []string{
		strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"unexpected":true`, 1),
		strings.Replace(string(raw), `"failures":0`, `"failures":11`, 1),
		strings.Replace(string(raw), strings.Repeat("c", 64), "not-a-digest", 1),
		strings.Replace(string(raw), `"entries":[`, `"Entries":[`, 1),
	} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadUsagePolling(dir); err == nil {
			t.Fatal("ambiguous/malformed settings accepted")
		}
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateUsagePolling(dir, func(f *UsagePolling) error { f.Entries = append(f.Entries, pollFixture()); return nil }); err == nil {
		t.Fatal("duplicate selected account accepted")
	}
	afterRaw, _ := os.ReadFile(path)
	if string(afterRaw) != string(raw) {
		t.Fatal("failed update replaced valid settings")
	}
}
