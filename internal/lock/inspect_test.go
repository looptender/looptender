package lock

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInspectAbsentAndInvalidNamesCreateNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "absent")
	for _, name := range []string{Tick, Daemon} {
		got := Inspect(dir, name)
		if got.Availability != "none" || got.Reason != "lock_file_absent" || got.Metadata != "not_recorded" {
			t.Fatalf("%+v", got)
		}
	}
	got := Inspect(dir, "../../PRIVATE")
	if got.Availability != "unavailable" || got.Name != "" || got.Reason != "unsupported_lock" {
		t.Fatalf("%+v", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("inspection created files: %v %v", entries, err)
	}
}

func TestInspectExistingFileAndUntrustedMetadata(t *testing.T) {
	for _, content := range []string{"", "PRIVATE /private/example credential=https://example.com/secret\n\x1b[31m", `{"pid":42,"run":"run_fictional"}`} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, Tick+".lock")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got := Inspect(dir, Tick)
			if runtime.GOOS == "darwin" {
				if got.Availability != "none" || got.Reason != "no_lock_held" {
					t.Fatalf("%+v", got)
				}
				want := "not_recorded"
				if content != "" {
					want = "stale"
				}
				if got.Metadata != want {
					t.Fatalf("%+v", got)
				}
			} else if runtime.GOOS == "linux" {
				if got.Availability != "unavailable" || got.Reason != "no_visible_lock" {
					t.Fatalf("%+v", got)
				}
			} else if got.Availability != "unavailable" || got.Reason != "read_only_lock_query_unsupported" {
				t.Fatalf("%+v", got)
			}
			raw, _ := json.Marshal(got)
			for _, private := range []string{"PRIVATE", "/private/", "credential", "run_fictional"} {
				if strings.Contains(string(raw), private) {
					t.Fatalf("leaked %s", private)
				}
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatal("inspection changed lock file")
			}
			// Inspection must leave an unlocked file available to a writer.
			l, err := Acquire(dir)
			if err != nil {
				t.Fatal(err)
			}
			l.Release()
		})
	}
}

func TestInspectRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, Tick+".lock"), 0700); err != nil {
		t.Fatal(err)
	}
	got := Inspect(dir, Tick)
	if got.Availability != "unavailable" || got.Reason != "lock_not_regular" || got.Metadata != "unreadable" {
		t.Fatalf("%+v", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(filepath.Join(dir, Tick+".lock"), filepath.Join(dir, Daemon+".lock")); err != nil {
		t.Fatal(err)
	}
	got = Inspect(dir, Daemon)
	if got.Availability != "unavailable" || got.Reason != "lock_not_regular" {
		t.Fatalf("%+v", got)
	}
}

// A second process observes a held lock; it must neither acquire nor release
// the parent's lock. This exercises real OS semantics, rather than a fake query.
func TestInspectHeldAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{Tick, Daemon} {
		l, err := AcquireNamed(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestInspectChild$")
		cmd.Env = append(os.Environ(), "INSPECT_CHILD_DIR="+dir, "INSPECT_CHILD_NAME="+name)
		out, err := cmd.CombinedOutput()
		if err != nil {
			l.Release()
			t.Fatalf("child: %v %s", err, out)
		}
		if contender, err := AcquireNamed(dir, name); err != ErrHeld {
			if contender != nil {
				contender.Release()
			}
			l.Release()
			t.Fatalf("inspection disturbed held lock: %v", err)
		}
		l.Release()
		got := Inspect(dir, name)
		if runtime.GOOS == "darwin" && got.Availability != "none" {
			t.Fatalf("release: %+v", got)
		}
	}
}

func TestInspectChild(t *testing.T) {
	dir := os.Getenv("INSPECT_CHILD_DIR")
	if dir == "" {
		t.Skip("subprocess fixture")
	}
	got := Inspect(dir, os.Getenv("INSPECT_CHILD_NAME"))
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if got.Availability != "held" || got.Reason != "holder_reference_unavailable" {
			t.Fatalf("%+v", got)
		}
	} else if got.Availability != "unavailable" || got.Reason != "read_only_lock_query_unsupported" {
		t.Fatalf("%+v", got)
	}
}

func TestInspectUnreadablePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	got := Inspect(file, Tick)
	if got.Availability != "unavailable" || got.Reason != "lock_unreadable" || got.Metadata != "unreadable" {
		t.Fatalf("%+v", got)
	}
}
