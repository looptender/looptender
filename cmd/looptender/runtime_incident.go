package main

import (
	"time"

	"github.com/looptender/looptender/internal/incident"
)

// Diagnostic writes never change admission or shutdown results, and never expose
// storage errors (which may contain private paths). Dry runs still run a daemon
// and attempt checks; the marker distinguishes them from supervision execution.
func recordRuntimeEvent(e env, dir, kind, reason, signal string, dry bool, outcome string) {
	if err := incident.Append(dir, incident.Record{Kind: kind, Runtime: &incident.RuntimeEvent{Reason: reason, Signal: signal}, CheckStartedAt: time.Now().UTC(), Build: version, DryRun: dry, Outcome: outcome}); err != nil {
		e.errf("warning: runtime incident history unavailable; diagnostic evidence is incomplete")
	}
}
