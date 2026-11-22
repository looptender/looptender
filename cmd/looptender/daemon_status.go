package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/state"
)

type daemonStatus struct {
	processID              int
	invocationID           string
	ConfigUnavailableSince *time.Time `json:"configUnavailableSince,omitempty"`
	State                  string     `json:"state"` // responsive, not_recorded, unavailable, skipped
	Message                string     `json:"message"`
	Phase                  string     `json:"phase,omitempty"`
	StartedAt              *time.Time `json:"startedAt,omitempty"`
	ObservedAt             *time.Time `json:"observedAt,omitempty"`
	NextCheckAt            *time.Time `json:"nextCheckAt,omitempty"`
	LastPassAt             *time.Time `json:"lastPassAt,omitempty"`
	LastPassStartedAt      *time.Time `json:"lastPassStartedAt,omitempty"`
	NextCheck              string     `json:"nextCheck"`
	// CheckedBy is "ticks" when no daemon answers and recent checks show
	// the loop is ticked by an external scheduler or by hand; Cadence is
	// how often recent checks ran (past checks, not a forecast). Both are
	// set only then (noteTickCadence).
	CheckedBy        string `json:"checkedBy,omitempty"`
	Cadence          string `json:"cadence,omitempty"`
	BinaryComparison string `json:"binaryComparison"`
	// version is the build label the daemon reported, made safe to show.
	// Status JSON carries only the comparison for the daemon's label; the
	// panel's Versions view reads the label. The last recorded check's label
	// and the installed build's are in runtime.recordedBuild and
	// runtime.installedBuild, in every mode including client status and
	// private mode, after a shape check (see shortBuildLabel).
	version string
	DryRun  bool `json:"dryRun"`
	// Adoption is what the daemon said about following new installs
	// (daemoninfo.Snapshot.Adoption); empty from an older daemon.
	Adoption string `json:"adoption,omitempty"`
}

func inspectDaemon(ctx context.Context, dir, current string, offline bool) daemonStatus {
	d := daemonStatus{State: "unavailable", Message: "The daemon endpoint could not be verified. External schedulers and older daemons are not inspected.", NextCheck: unknownNextCheck, BinaryComparison: "unknown"}
	if offline {
		d.State, d.Message = "skipped", "Live daemon query skipped in offline mode; external schedulers are not inspected."
		return d
	}
	s, err := daemoninfo.Query(ctx, dir)
	if errors.Is(err, os.ErrNotExist) {
		d.State, d.Message = "not_recorded", "No daemon endpoint is recorded. This does not rule out an external scheduler or an older daemon."
		return d
	}
	if err != nil {
		return d
	}
	d.processID, d.invocationID = s.ProcessID, s.InvocationID
	d.State, d.Message = "responsive", "A looptender daemon answered a fresh local challenge. This does not establish agent health or future execution."
	d.Phase, d.StartedAt, d.ObservedAt, d.NextCheckAt, d.LastPassAt, d.LastPassStartedAt, d.DryRun = s.Phase, &s.StartedAt, &s.ObservedAt, s.NextCheckAt, s.LastPassAt, s.LastPassStartedAt, s.DryRun
	d.BinaryComparison = compareCheckVersion(&state.Check{Version: s.Version}, current)
	d.version = dockBuildLabel(s.Version)
	d.Adoption = s.Adoption
	d.NextCheck = "check in progress; the next interval starts after this pass finishes"
	if s.Phase == "waiting" && s.NextCheckAt != nil {
		d.NextCheck = "normally due " + s.NextCheckAt.UTC().Format(time.RFC3339) + "; control changes can trigger an earlier check"
	}
	if s.ConfigUnavailableSince != nil {
		d.ConfigUnavailableSince = s.ConfigUnavailableSince
		d.Message = "The daemon cannot load its configuration file since " + s.ConfigUnavailableSince.UTC().Format(time.RFC3339) + "; ordinary checks are stopped. A saved stop may use the last configuration read."
		d.NextCheck = "ordinary checks wait for a readable, valid configuration file"
	}
	return d
}
