//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
)

func ownedByAnother(t *testing.T, f *fixture) {
	t.Helper()
	if code, _, errs := f.run("enable"); code != 0 {
		t.Fatal(errs)
	}
	ctl, _ := control.Load(filepath.Join(f.dir, "state"))
	ctl.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 2}
	if err := control.Save(filepath.Join(f.dir, "state"), ctl); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_THREAD_ID", "thr_example_caller")
}

// validate by a thread that does not own the loop still checks the
// configuration, says so, and changes nothing; it does not advise a take-over.
func TestValidateByANonOwnerChecksTheConfiguration(t *testing.T) {
	f := newFixture(t)
	ownedByAnother(t, f)
	before := fmt.Sprint(transferFiles(t, filepath.Join(f.dir, "state")))
	code, out, errs := f.run("validate")
	if code != 0 || !strings.Contains(out, "ok: ") || !strings.Contains(out, "this caller is not the owner") || strings.Contains(out+errs, "use enable --take-over from the new owning thread") {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if strings.Contains(out, "thr_example_owner") {
		t.Fatalf("the owner's thread ID is printed: %s", out)
	}
	if fmt.Sprint(transferFiles(t, filepath.Join(f.dir, "state"))) != before {
		t.Fatal("validate changed the state directory")
	}
	// A broken configuration still fails for the same caller.
	if err := os.WriteFile(f.cfg, []byte("version: 1\nloop:\n  name: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.run("validate"); code == 0 {
		t.Fatal("a broken configuration validated")
	}
}

// context says when the caller is not the owner, and not otherwise.
func TestContextSaysWhenTheCallerIsNotTheOwner(t *testing.T) {
	f := newFixture(t)
	ownedByAnother(t, f)
	code, out, errs := f.run("context")
	if code != 0 || !strings.Contains(out, "this caller is not the owner: the control commands below would be refused") {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	_, js, _ := f.run("context", "--json")
	if !strings.Contains(js, `"callerStatus": "not_owner"`) && !strings.Contains(js, `"callerStatus":"not_owner"`) {
		t.Fatalf("%s", js)
	}
	t.Setenv("BB_THREAD_ID", "thr_example_owner")
	if _, out, _ := f.run("context"); strings.Contains(out, "not the owner") {
		t.Fatalf("the owner is told it is not the owner:\n%s", out)
	}
	t.Setenv("BB_THREAD_ID", "")
	if _, js, _ := f.run("context", "--json"); !strings.Contains(js, "no_identity") {
		t.Fatalf("%s", js)
	}
}

// validate with a state directory the caller cannot write still checks the
// configuration and says why the owner was not read.
func TestValidateWithAnUnwritableStateDirectoryChecksTheConfiguration(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != 0 {
		t.Fatal(errs)
	}
	dir := filepath.Join(f.dir, "state")
	os.Remove(filepath.Join(dir, "control.lock"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	code, out, errs := f.run("validate")
	if code != 0 || !strings.Contains(out, "ok: ") || !strings.Contains(out, "ownership: not checked: the state directory is not writable for this caller") {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
}
