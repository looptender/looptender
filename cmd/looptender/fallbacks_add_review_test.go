package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/lock"
)

func reviewedAddOptions(f addFixture) fallbackAddOptions {
	return fallbackAddOptions{cfgPath: f.cfg, agent: "claude", id: "route-frozen", position: 1, model: "example-added", group: "pool-added", probeCap: 2, set: map[string]bool{}, panelReviewed: true, panelScheduledDisabled: true}
}
func TestReviewedAddCapturesActualPlannerABAReads(t *testing.T) {
	for _, source := range []string{"config", "fallbacks"} {
		t.Run(source, func(t *testing.T) {
			f := newAddFixture(t, "--fallback", "claude:pool-existing")
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := f.cfg
			if source == "fallbacks" {
				path = f.fallbacks
			}
			original := fallbackAddReadFile
			t.Cleanup(func() { fallbackAddReadFile = original })
			calls := 0
			fallbackAddReadFile = func(p string) ([]byte, error) {
				if p != path {
					return original(p)
				}
				calls++
				a, err := os.ReadFile(p)
				if err != nil {
					return nil, err
				}
				b := append(append([]byte{}, a...), []byte("# actual transient planner input\n")...)
				if err := os.WriteFile(p, b, 0600); err != nil {
					return nil, err
				}
				consumed, err := original(p)
				if restoreErr := os.WriteFile(p, a, 0600); restoreErr != nil {
					t.Fatal(restoreErr)
				}
				return consumed, err
			}
			if _, _, err := planReviewedFallbackAdd(cfg, reviewedAddOptions(f), f.lookPath); err == nil {
				t.Fatal("mixed/ABA planner read certified")
			}
			if calls != 1 {
				t.Fatal("did not exercise actual planner read", calls)
			}
			if err := cfg.CheckSources(); err != nil {
				t.Fatal("fixture did not restore A", err)
			}
		})
	}
}
func TestReviewedAddDigestPinsActualResultAndFrozenInputs(t *testing.T) {
	f := newAddFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	o := reviewedAddOptions(f)
	p, before, err := planReviewedFallbackAdd(cfg, o, f.lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if p.fallbackRead.Present || p.configRead.Size == 0 || !p.rules.Probe.Disabled {
		t.Fatal("preimages or creation policy missing")
	}
	other := o
	other.model = "example-different"
	_, after, err := planReviewedFallbackAdd(cfg, other, f.lookPath)
	if err != nil || after == before {
		t.Fatal("changed plan did not change digest", err)
	}
	// Existing lists ignore the creation-only policy option and internal cap;
	// CLI creation keeps its historical scheduled default.
	cli := o
	cli.panelScheduledDisabled = false
	cli.panelReviewed = false
	q, err := planFallbackAdd(cfg, cli, f.lookPath)
	if err != nil || q.rules.Probe.Disabled {
		t.Fatal("CLI default changed", err)
	}
}

type addFailureFile struct {
	*os.File
	failure string
}

func (f addFailureFile) Write(b []byte) (int, error) {
	if f.failure == "write" {
		n, _ := f.File.Write(b[:len(b)/2])
		return n, errors.New("fictional partial write")
	}
	return f.File.Write(b)
}
func (f addFailureFile) Sync() error {
	if f.failure == "sync" {
		return errors.New("fictional sync failure")
	}
	return f.File.Sync()
}
func (f addFailureFile) Close() error {
	err := f.File.Close()
	if f.failure == "close" {
		return errors.New("fictional close failure")
	}
	return err
}
func (f addFailureFile) Stat() (os.FileInfo, error) {
	if f.failure == "stat" {
		return nil, errors.New("fictional stat failure")
	}
	return f.File.Stat()
}
func TestReviewedAddPublicationCleanupAndHistory(t *testing.T) {
	for _, failure := range []string{"create", "write", "sync", "close", "stat", "config", "cleanup", "partial_cleanup", "substitute", "history"} {
		t.Run(failure, func(t *testing.T) {
			f := newAddFixture(t)
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			p, err := planFallbackAdd(cfg, reviewedAddOptions(f), f.lookPath)
			if err != nil {
				t.Fatal(err)
			}
			oldCreate, oldReplace, oldRemove, oldHistory := fallbackAddCreate, fallbackAddReplace, fallbackAddRemove, fallbackAddHistory
			t.Cleanup(func() {
				fallbackAddCreate = oldCreate
				fallbackAddReplace = oldReplace
				fallbackAddRemove = oldRemove
				fallbackAddHistory = oldHistory
			})
			fallbackAddCreate = func(path string) (fallbackAddFile, error) {
				if failure == "create" {
					return nil, errors.New("fictional create failure")
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				mode := failure
				if mode == "partial_cleanup" {
					mode = "write"
				}
				return addFailureFile{file, mode}, nil
			}
			fallbackAddReplace = func(path string, b []byte) error {
				if path == f.cfg && (failure == "config" || failure == "cleanup" || failure == "substitute") {
					if failure == "substitute" {
						if err := os.Rename(f.fallbacks, f.fallbacks+".owned"); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(f.fallbacks, []byte("# another writer's file\n"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					return errors.New("fictional config failure")
				}
				return oldReplace(path, b)
			}
			if failure == "cleanup" || failure == "partial_cleanup" {
				fallbackAddRemove = func(string) error { return errors.New("fictional cleanup failure") }
			}
			histories := 0
			fallbackAddHistory = func(dir, op, id, caller string) error {
				histories++
				if failure == "history" {
					return errors.New("fictional history failure")
				}
				return oldHistory(dir, op, id, caller)
			}
			held, err := lock.Acquire(cfg.Loop.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Release()
			before, _ := os.ReadFile(f.cfg)
			result, writeErr := commitFallbackAddLocked(p, "")
			partial := failure == "cleanup" || failure == "partial_cleanup" || failure == "substitute" || failure == "stat"
			switch {
			case failure == "history":
				if writeErr != nil || result.Outcome != "changed" || !p.written || !p.historyFailed || histories != 1 {
					t.Fatalf("history: %+v %v", result, writeErr)
				}
			case partial:
				if writeErr == nil || result.Outcome != "partial" || result.Stage != "fallback_file_retained" || p.written || histories != 0 {
					t.Fatalf("partial falsely rolled back: %+v %v", result, writeErr)
				}
				b, err := os.ReadFile(f.fallbacks)
				if err != nil || (failure != "stat" && len(b) == 0) {
					t.Fatal("fixture did not retain file", err)
				}
				if failure == "substitute" && !strings.Contains(string(b), "another writer") {
					t.Fatal("removed substituted file")
				}
			default:
				if writeErr == nil || result.Outcome != "failed" || p.written || histories != 0 {
					t.Fatalf("failure: %+v %v", result, writeErr)
				}
				if _, err := os.Stat(f.fallbacks); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cleanup left unreported file", err)
				}
			}
			if failure != "history" {
				after, _ := os.ReadFile(f.cfg)
				if string(after) != string(before) {
					t.Fatal("failed creation changed config")
				}
			}
			// No history is emitted for an orphan that never became referenced.
		})
	}
}
