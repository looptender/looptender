package control

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Ownership is a durable claim, not a lease on the thread's current activity.
// Generation changes on transfer, fencing daemons even across A -> B -> A.
type Ownership struct {
	ThreadID   string   `json:"threadId"`
	Generation uint64   `json:"generation"`
	Previous   *Handoff `json:"previous,omitempty"`
	// Followed is set only by a move the supervisor made itself (a handoff
	// to a thread it created, or an automatic switch-back). It lets a daemon
	// bound to an earlier generation in the same unbroken run keep running.
	// Any explicit claim writes ownership without it, which ends the run.
	Followed *Followed `json:"followed,omitempty"`
}

// Followed describes an unbroken run of supervisor-made ownership moves.
// Base is the generation the run started from; From lists the threads that
// lost ownership in it, oldest first, at most MaxFollowedFrom.
type Followed struct {
	Base uint64   `json:"base"`
	From []string `json:"from,omitempty"`
}

// MaxFollowedFrom bounds Followed.From. A thread dropped from the list is
// refused like any other non-owner, which is the safe side.
const MaxFollowedFrom = 16

// StatusSupervisorHandoff is Handoff.Status for a move the supervisor made.
const StatusSupervisorHandoff = "supervisor_handoff"

// Handoff records the most recent transfer without mutating BB relationships.
type Handoff struct {
	ThreadID string    `json:"threadId"`
	Status   string    `json:"status"`
	At       time.Time `json:"at"`
}

// ErrOwnerConflict identifies a valid claim held by a different BB thread.
var ErrOwnerConflict = errors.New("loop ownership conflict")

var ownerID = regexp.MustCompile(`^thr_[A-Za-z0-9_-]{1,128}$`)

// CheckOwner is pure. Missing caller identity represents an external scheduler
// or terminal: it may use the existing claim, but cannot take it over.
func CheckOwner(f File, caller string, takeOver bool) error {
	if caller != "" && !ownerID.MatchString(caller) {
		return errors.New("invalid BB_THREAD_ID; expected a BB thread ID")
	}
	if takeOver && caller == "" {
		return errors.New("--take-over requires BB_THREAD_ID; run it from the new owning BB thread")
	}
	o := f.Ownership
	if o == nil {
		return nil
	}
	if !ownerID.MatchString(o.ThreadID) || o.Generation == 0 || !o.Followed.valid(o.Generation) {
		return errors.New("invalid loop ownership metadata; restore a valid control file before claiming this loop")
	}
	if caller != "" && caller != o.ThreadID && !takeOver {
		if o.FollowedFrom(caller) {
			at := ""
			if o.Previous != nil && o.Previous.Status == StatusSupervisorHandoff {
				at = " at " + o.Previous.At.UTC().Format(time.RFC3339)
			}
			return fmt.Errorf("%w: ownership of this loop moved from this thread to BB thread %s with the supervisor's handoff%s; this thread no longer owns the loop. Run commands for it from %s, or use enable --take-over only if a person asks", ErrOwnerConflict, o.ThreadID, at, o.ThreadID)
		}
		return fmt.Errorf("%w: loop is owned by BB thread %s; use enable --take-over from the new owning thread to transfer ownership", ErrOwnerConflict, o.ThreadID)
	}
	return nil
}

func (f *Followed) valid(generation uint64) bool {
	if f == nil {
		return true
	}
	if f.Base == 0 || f.Base >= generation || len(f.From) > MaxFollowedFrom {
		return false
	}
	for _, id := range f.From {
		if !ownerID.MatchString(id) {
			return false
		}
	}
	return true
}

// FollowedFrom reports whether thread lost ownership to a supervisor-made
// move in the current run.
func (o *Ownership) FollowedFrom(thread string) bool {
	if o == nil || o.Followed == nil {
		return false
	}
	for _, id := range o.Followed.From {
		if id == thread {
			return true
		}
	}
	return false
}

// Follows reports whether a claim bound at (thread, generation) is an earlier
// step of the current unbroken run of supervisor-made moves, so a daemon
// bound to it may adopt the current owner instead of exiting.
func (o *Ownership) Follows(thread string, generation uint64) bool {
	if o == nil || o.Followed == nil || thread == "" {
		return false
	}
	return generation >= o.Followed.Base && generation < o.Generation
}

// FollowHandoff moves ownership with a move the supervisor made itself. It
// applies only when the loop is owned, ready, and still owned by from at
// fromGeneration: anything else means an explicit claim came in between,
// which wins, and moved is false. Holds and every other field are kept.
func FollowHandoff(f *File, from string, fromGeneration uint64, to string, at time.Time) (moved bool, err error) {
	if err := CheckTransferReady(*f); err != nil {
		return false, err
	}
	if !ownerID.MatchString(to) {
		return false, errors.New("invalid BB thread for the new owner")
	}
	if err := CheckOwner(*f, "", false); err != nil {
		return false, err
	}
	old := f.Ownership
	if old == nil || old.ThreadID != from || old.Generation != fromGeneration || from == to {
		return false, nil
	}
	if old.Generation == ^uint64(0) {
		return false, errors.New("loop ownership generation exhausted")
	}
	run := &Followed{Base: old.Generation}
	if old.Followed != nil {
		run.Base = old.Followed.Base
		run.From = append(run.From, old.Followed.From...)
	}
	kept := run.From[:0]
	for _, id := range run.From {
		if id != from && id != to {
			kept = append(kept, id)
		}
	}
	run.From = append(kept, from)
	if len(run.From) > MaxFollowedFrom {
		run.From = run.From[len(run.From)-MaxFollowedFrom:]
	}
	f.Ownership = &Ownership{ThreadID: to, Generation: old.Generation + 1, Followed: run,
		Previous: &Handoff{ThreadID: from, Status: StatusSupervisorHandoff, At: at.UTC()}}
	return true, nil
}

// ClaimOwner runs inside UpdateChecked and under the tick lock. It preserves
// every control hold. Same-owner calls do not change the generation.
func ClaimOwner(f *File, caller string, takeOver bool) error {
	if err := CheckTransferReady(*f); err != nil {
		return err
	}
	if err := CheckOwner(*f, caller, takeOver); err != nil {
		return err
	}
	if caller == "" {
		return nil
	}
	if f.Ownership == nil {
		f.Ownership = &Ownership{ThreadID: caller, Generation: 1}
		return nil
	}
	old := f.Ownership
	if old.ThreadID == caller {
		return nil
	}
	if old.Generation == ^uint64(0) {
		return errors.New("loop ownership generation exhausted")
	}
	f.Ownership = &Ownership{ThreadID: caller, Generation: old.Generation + 1, Previous: &Handoff{ThreadID: old.ThreadID, Status: "handed_off", At: time.Now().UTC()}}
	return nil
}
