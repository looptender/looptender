//go:build darwin || linux

package main

import (
	"bytes"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompletionRenewalSpecialFilesAndBounds(t *testing.T) {
	for _, record := range []string{"request", "control", "state", "goal", "ticket"} {
		for _, kind := range []string{"fifo", "symlink", "directory", "oversize", "public"} {
			t.Run(record+"/"+kind, func(t *testing.T) {
				f := newRenewalFixture(t)
				path := f.request
				op := "prepare-completion-renewal"
				ticket, review := "", ""
				switch record {
				case "control":
					path = filepath.Join(f.entry.StateDir, control.FileName)
				case "state":
					path = filepath.Join(f.entry.StateDir, "state.json")
				case "goal":
					path = filepath.Join(f.entry.StateDir, control.GoalName)
				case "ticket":
					p := f.prepare(t)
					ticket, review = p.Ticket, p.ReviewDigest
					op = "apply-completion-renewal"
					path = filepath.Join(f.entry.StateDir, renewalTicketFile)
				}
				_ = os.Remove(path)
				switch kind {
				case "fifo":
					if e := unix.Mkfifo(path, 0600); e != nil {
						t.Fatal(e)
					}
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					resumeWrite(t, target, []byte("{}"))
					if e := os.Symlink(target, path); e != nil {
						t.Fatal(e)
					}
				case "directory":
					if e := os.Mkdir(path, 0700); e != nil {
						t.Fatal(e)
					}
				case "oversize":
					resumeWrite(t, path, make([]byte, freezeWireLimit+1))
				case "public":
					resumeWrite(t, path, []byte("{}"))
					if e := os.Chmod(path, 0644); e != nil {
						t.Fatal(e)
					}
				}
				start := time.Now()
				r, c := f.call(op, ticket, review, renewalFaults{})
				if c == 0 || time.Since(start) > 2*time.Second {
					t.Fatal("unsafe or blocking input", c, r)
				}
			})
		}
	}
	// A device is never accepted as a request, without reading it.
	if _, e := renewalRead("/dev/null", 32768); e == nil {
		t.Fatal("accepted device")
	}
}

func TestCompletionRenewalArchivePresence(t *testing.T) {
	for _, op := range []string{"prepare-completion-renewal", "apply-completion-renewal"} {
		for _, kind := range []string{"fifo", "symlink", "directory", "malformed", "oversize", "unreadable"} {
			t.Run(op+"/"+kind, func(t *testing.T) {
				f := newRenewalFixture(t)
				ticket, review := "", ""
				if op == "apply-completion-renewal" {
					p := f.prepare(t)
					ticket, review = p.Ticket, p.ReviewDigest
				}
				path := filepath.Join(f.entry.StateDir, control.ArchiveFileName)
				switch kind {
				case "fifo":
					if err := unix.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(filepath.Join(t.TempDir(), "absent-target"), path); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "malformed":
					resumeWrite(t, path, []byte("{"))
				case "oversize":
					resumeWrite(t, path, make([]byte, freezeWireLimit+1))
				case "unreadable":
					resumeWrite(t, path, []byte("{}"))
					if err := os.Chmod(path, 0000); err != nil {
						t.Fatal(err)
					}
				}
				before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
				done := make(chan struct{})
				var r renewalReport
				var c int
				go func() { defer close(done); r, c = f.call(op, ticket, review, renewalFaults{}) }()
				blocked := false
				select {
				case <-done:
				case <-time.After(500 * time.Millisecond):
					blocked = true
					// Release only the owned no-writer FIFO under a guard-removal mutant,
					// so the failed regression never leaves an orphan reader holding locks.
					if kind == "fifo" {
						writer, err := os.OpenFile(path, os.O_WRONLY, 0)
						if err != nil {
							t.Fatal(err)
						}
						writer.Close()
					}
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Fatal("archive refusal did not terminate")
					}
				}
				if blocked || c == 0 || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
					t.Fatal("archive path was opened or accepted", blocked, c, r)
				}
				// Refusal releases the registry/tick/control locks, not just the caller.
				for _, name := range []string{lock.Tick, lock.Control} {
					held, err := lock.AcquireNamed(f.entry.StateDir, name)
					if err != nil {
						t.Fatal("refusal retained lock", name, err)
					}
					held.Release()
				}
				if op == "apply-completion-renewal" {
					saved, err := loadRenewalTicket(f.entry.StateDir)
					if err != nil || saved.Consumed {
						t.Fatal("archive refusal consumed ticket", saved, err)
					}
				}
			})
		}
	}
}

func TestCompletionRenewalArchiveAppearsAfterConsumption(t *testing.T) {
	f := newRenewalFixture(t)
	p := f.prepare(t)
	path := filepath.Join(f.entry.StateDir, control.ArchiveFileName)
	before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
	r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{AfterConsume: func() error { return unix.Mkfifo(path, 0600) }})
	if c == 0 || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
		t.Fatal("post-consumption archive changed control", c, r)
	}
	saved, err := loadRenewalTicket(f.entry.StateDir)
	if err != nil || !saved.Consumed {
		t.Fatal("consumed ticket was restored", saved, err)
	}
}
