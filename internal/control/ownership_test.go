package control

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/lock"
)

func TestOwnershipClaimTransferAndHolds(t *testing.T) {
	f := File{Enabled: false, Paused: true, Stop: true, AwaitingUser: true, Completed: true, Note: "fictional hold"}
	if err := ClaimOwner(&f, "thr_first", false); err != nil {
		t.Fatal(err)
	}
	first := *f.Ownership
	if first.ThreadID != "thr_first" || first.Generation != 1 || first.Previous != nil {
		t.Fatalf("%+v", first)
	}
	if err := ClaimOwner(&f, "thr_second", false); err == nil || !reflect.DeepEqual(*f.Ownership, first) {
		t.Fatal("conflict changed owner")
	}
	if err := ClaimOwner(&f, "thr_first", false); err != nil || !reflect.DeepEqual(*f.Ownership, first) {
		t.Fatal("same owner changed generation")
	}
	for n, id := range []string{"thr_second", "thr_first"} {
		old := f.Ownership.ThreadID
		if err := ClaimOwner(&f, id, true); err != nil {
			t.Fatal(err)
		}
		o := f.Ownership
		if o.ThreadID != id || o.Generation != uint64(n+2) || o.Previous.ThreadID != old || o.Previous.Status != "handed_off" || o.Previous.At.IsZero() {
			t.Fatalf("%+v", o)
		}
		if f.Enabled || !f.Paused || !f.Stop || !f.AwaitingUser || !f.Completed || f.Note != "fictional hold" {
			t.Fatal("takeover changed controls")
		}
	}
}

func TestOwnershipValidationAndSchedulerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, caller  string
		owner         *Ownership
		take, wantErr bool
	}{
		{"legacy_scheduler", "", nil, false, false},
		{"new_owner", "thr_new", nil, false, false},
		{"scheduler_existing", "", &Ownership{ThreadID: "thr_old", Generation: 1}, false, false},
		{"conflict", "thr_new", &Ownership{ThreadID: "thr_old", Generation: 1}, false, true},
		{"explicit_transfer", "thr_new", &Ownership{ThreadID: "thr_old", Generation: 1}, true, false},
		{"anonymous_transfer", "", &Ownership{ThreadID: "thr_old", Generation: 1}, true, true},
		{"invalid_identity", "private fixture value", nil, false, true},
		{"invalid_owner", "thr_new", &Ownership{ThreadID: "private fixture value", Generation: 1}, true, true},
		{"invalid_generation", "thr_new", &Ownership{ThreadID: "thr_old"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := File{Ownership: tc.owner}
			if err := CheckOwner(f, tc.caller, tc.take); (err != nil) != tc.wantErr {
				t.Fatalf("%v", err)
			}
		})
	}
	f := File{Ownership: &Ownership{ThreadID: "thr_old", Generation: ^uint64(0)}}
	if err := ClaimOwner(&f, "thr_new", true); err == nil {
		t.Fatal("generation overflow accepted")
	}
}

func TestControlUpdatesSerializeAndPreserveOwnership(t *testing.T) {
	dir := t.TempDir()
	if _, err := UpdateChecked(dir, func(f *File) error { return ClaimOwner(f, "thr_first", false) }); err != nil {
		t.Fatal(err)
	}
	l, err := lock.AcquireNamed(dir, lock.Control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(dir, func(f *File) { f.Paused = true }); !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("writer bypassed lock: %v", err)
	}
	l.Release()
	if _, err := Update(dir, func(f *File) { f.Paused = true; f.Stop = true }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, FileName))
	if _, err := UpdateChecked(dir, func(f *File) error { f.Paused = false; return ClaimOwner(f, "thr_second", false) }); err == nil {
		t.Fatal("unapproved transfer")
	}
	after, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(before) != string(after) {
		t.Fatal("rejected update wrote control")
	}
	f, err := UpdateChecked(dir, func(f *File) error { return ClaimOwner(f, "thr_second", true) })
	if err != nil || !f.Stop || !f.Paused || f.Ownership.ThreadID != "thr_second" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestFollowHandoffRunAndExplicitEnd(t *testing.T) {
	f := File{Ownership: &Ownership{ThreadID: "thr_a", Generation: 4}, Paused: true}
	if moved, err := FollowHandoff(&f, "thr_other", 4, "thr_b", time.Now()); moved || err != nil {
		t.Fatalf("moved from a thread that does not own the loop: %v %v", moved, err)
	}
	if moved, err := FollowHandoff(&f, "thr_a", 3, "thr_b", time.Now()); moved || err != nil {
		t.Fatal("moved at a stale generation")
	}
	if moved, err := FollowHandoff(&f, "thr_a", 4, "thr_b", time.Now()); !moved || err != nil {
		t.Fatal(err)
	}
	if moved, _ := FollowHandoff(&f, "thr_b", 5, "thr_a", time.Now()); !moved {
		t.Fatal("switch-back did not move")
	}
	o := f.Ownership
	if !f.Paused || o.ThreadID != "thr_a" || o.Generation != 6 || o.Followed.Base != 4 || len(o.Followed.From) != 1 || o.Followed.From[0] != "thr_b" ||
		!o.Follows("thr_a", 4) || !o.Follows("thr_b", 5) || o.Follows("thr_a", 3) || o.Follows("thr_a", 6) {
		t.Fatalf("%+v %+v", o, o.Followed)
	}
	if CheckOwner(f, "", false) != nil {
		t.Fatal("valid run refused")
	}
	bad := f
	bad.Ownership = &Ownership{ThreadID: "thr_a", Generation: 6, Followed: &Followed{Base: 6}}
	if CheckOwner(bad, "", false) == nil {
		t.Fatal("run base at the current generation accepted")
	}
	if err := BindBBTarget(&f, "thr_a"); err != nil || f.Ownership.Followed != nil || f.Ownership.Generation != 7 {
		t.Fatalf("explicit move kept the run: %+v %v", f.Ownership, err)
	}
	f.Ownership = &Ownership{ThreadID: "thr_a", Generation: 1}
	FollowHandoff(&f, "thr_a", 1, "thr_b", time.Now())
	if err := ClaimOwner(&f, "thr_c", true); err != nil || f.Ownership.Followed != nil {
		t.Fatalf("take-over kept the run: %+v", f.Ownership)
	}
}

// An explicit take-over by the current owner ends a supervisor-made run.
func TestSameOwnerTakeOverEndsTheRun(t *testing.T) {
	f := File{Ownership: &Ownership{ThreadID: "thr_a", Generation: 1}}
	FollowHandoff(&f, "thr_a", 1, "thr_b", time.Now())
	if err := ClaimOwner(&f, "thr_b", false); err != nil || f.Ownership.Followed == nil {
		t.Fatal("a plain enable by the owner ended the run")
	}
	if err := ClaimOwner(&f, "thr_b", true); err != nil || f.Ownership.Followed != nil || f.Ownership.Generation != 2 || f.Ownership.FollowedFrom("thr_a") || f.Ownership.Follows("thr_a", 1) {
		t.Fatalf("%+v %v", f.Ownership, err)
	}
}
