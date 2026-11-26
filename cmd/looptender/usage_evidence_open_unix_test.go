//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageEvidenceReplacementCannotBlock(t *testing.T) {
	if dir := os.Getenv("USAGE_FIFO_FIXTURE"); dir != "" {
		got := loadUsageEvidenceWithOpen(dir, "example", usageFixture().To, func(p string) (*os.File, error) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(p, 0600); err != nil {
				t.Fatal(err)
			}
			return openUsageEvidence(p)
		})
		if got.Availability != "unavailable" {
			t.Fatal("substituted FIFO accepted")
		}
		return
	}
	dir := t.TempDir()
	raw, _ := json.Marshal(usageFixture())
	if err := os.WriteFile(filepath.Join(dir, "usage-evidence.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestUsageEvidenceReplacementCannotBlock$")
	cmd.Env = append(os.Environ(), "USAGE_FIFO_FIXTURE="+dir)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("replacement FIFO blocked reader; owned test process killed at deadline")
	}
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestUsageEvidenceOpenedIdentityAndType(t *testing.T) {
	for _, kind := range []string{"replacement", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "usage-evidence.json")
			raw, _ := json.Marshal(usageFixture())
			if err := os.WriteFile(p, raw, 0600); err != nil {
				t.Fatal(err)
			}
			got := loadUsageEvidenceWithOpen(dir, "example", usageFixture().To, func(path string) (*os.File, error) {
				replacement := filepath.Join(dir, "new.json")
				data := raw
				if kind == "oversized" {
					data = make([]byte, usageEvidenceLimit+1)
				}
				if err := os.WriteFile(replacement, data, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(replacement, path); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Rename(replacement, path); err != nil {
						t.Fatal(err)
					}
				}
				return openUsageEvidence(path)
			})
			if got.Availability != "unavailable" {
				t.Fatal("replacement was accepted", got)
			}
		})
	}
}
