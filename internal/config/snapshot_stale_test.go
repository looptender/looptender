package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkSourcesStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "looptender.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nloop:\n  name: example-loop\n  state_dir: state\n  adapter: process\n  process: {status_command: [x], resume_command: [x], stop_command: [x]}\n  prompt: Continue the fictional example.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckSources(); err != nil {
		t.Fatal(err)
	}
	c.MarkSourcesStale()
	if c.CheckSources() == nil {
		t.Fatal("stale sources still verified")
	}
}
