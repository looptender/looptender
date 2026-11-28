//go:build darwin || linux

package main

import (
	"github.com/looptender/looptender/internal/control"
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
