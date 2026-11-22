package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/daemoninfo"
)

// adoptStalled is how long a recorded switch may go without a daemon
// answering before status says no supervisor may be running.
const adoptStalled = 2 * time.Minute

// buildAdoptionStatus is the last build change of the loop's daemon, from
// its record, read against whether a daemon answers now.
type buildAdoptionStatus struct {
	Outcome string    `json:"outcome"` // switching, adopted, refused, returned, failed, unreadable
	At      time.Time `json:"at,omitempty"`
	From    string    `json:"fromSha256,omitempty"`
	To      string    `json:"toSha256,omitempty"`
	// Supervised is false when the record says no daemon is running.
	Supervised bool   `json:"supervised"`
	Message    string `json:"message"`
}

func adoptBuildName(b adoptBuildID) string {
	label := dockBuildLabel(b.Label)
	if label == "" {
		label = "unlabelled build"
	}
	if b.SHA256 == "" {
		return label
	}
	return label + " (" + shortDigest(b.SHA256) + ")"
}

// describeBuildAdoption returns nil when the loop's daemon has no recorded
// build change.
func describeBuildAdoption(dir string, daemon daemonStatus, now time.Time) *buildAdoptionStatus {
	r, err := loadAdoptRecord(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &buildAdoptionStatus{Outcome: "unreadable", Supervised: true, Message: "The record of the daemon's last build change could not be read."}
	}
	s := &buildAdoptionStatus{Outcome: r.Outcome, At: r.At, From: shortDigest(r.From.SHA256), To: shortDigest(r.To.SHA256), Supervised: true}
	at := r.At.UTC().Format(time.RFC3339)
	from, to := adoptBuildName(r.From), adoptBuildName(r.To)
	answers := daemon.State == "responsive"
	// The build the record names never came up: a daemon is recorded but
	// does not answer, and its record is older than the change, so no daemon
	// started after it. A daemon that started and later stopped (a crash, a
	// reboot) is no longer the switch's doing; one that ended on purpose
	// removes its record and reads as not recorded.
	dead := daemon.State == "unavailable" && now.Sub(r.At) >= adoptStalled && endpointBefore(dir, r.At)
	switch r.Outcome {
	case adoptAdopted:
		s.Message = fmt.Sprintf("The daemon switched from %s to the installed %s at %s, after a check; holds, owner, send counts and state were kept.", from, to, at)
		if dead {
			s.Supervised = false
			s.Message += " No daemon answers since, though one is recorded: the new build may have exited, and no supervisor may be running. Start supervision again."
		}
	case adoptSwitching:
		if answers || now.Sub(r.At) < adoptStalled {
			s.Message = fmt.Sprintf("The daemon began switching from %s to the installed %s at %s.", from, to, at)
		} else {
			s.Supervised = false
			s.Message = fmt.Sprintf("The daemon began switching from %s to the installed %s at %s and no daemon answers since: no supervisor may be running. Start supervision again.", from, to, at)
		}
	case adoptRefused:
		s.Message = fmt.Sprintf("The daemon stays on %s and did not switch to the installed %s (%s): %s.", from, to, at, r.Reason)
	case adoptReturned:
		s.Message = fmt.Sprintf("The installed %s could not start at %s (%s); the daemon returned to %s.", from, at, r.Reason, to)
		if dead {
			s.Supervised = false
			s.Message += " No daemon answers since, though one is recorded: no supervisor may be running. Start supervision again."
		}
	case adoptHandedOff:
		s.Message = fmt.Sprintf("The loop's owner changed while the daemon was switching from %s to %s at %s; the daemon handed off and exited, as at any check.", from, to, at)
	case adoptFailed:
		s.Supervised = answers
		s.Message = fmt.Sprintf("Switching from %s to %s failed at %s: %s.", from, to, at, r.Reason)
	default:
		s.Outcome, s.Message = "unreadable", "The record of the daemon's last build change could not be read."
	}
	return s
}

// endpointBefore says whether the loop's recorded daemon endpoint was
// written before at; false when there is none or it cannot be read.
func endpointBefore(dir string, at time.Time) bool {
	info, err := os.Stat(filepath.Join(dir, daemoninfo.FileName))
	return err == nil && info.ModTime().Before(at)
}
