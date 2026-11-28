package jobplan

import "time"

// No IO, callbacks, real clock acquisition, JSON or prose interpretation belongs
// here. Supplied comparisons and recency are not authentication or admission.
func Preview(s Selection, o Observation, c Context) Report {
	r := Report{Confidence: "caller_supplied_unverified", Effects: EffectAvailability{"unavailable", "preview_only"}, Recommendation: "inspect_selected_source", Assessability: map[string]State{}}
	for _, name := range []string{"selection", "identity", "retrieval", "enabled", "definition", "owner", "owner_revision", "occurrence", "history", "last_run", "next_run", "result", "loop_binding", "relationship", "controls", "capabilities", "expected_definition", "expected_owner_revision"} {
		r.Assessability[name] = Unknown
	}
	if s.Key.State != Supplied {
		r.Recommendation = "pin_already_authorized_source"
	}
	add := func(code string) { r.Reasons = append(r.Reasons, code) }
	// Preserve restrictive facts even if another input is invalid or over bound.
	controls := c.Controls.Value
	flags := []struct {
		name        string
		field       Field[bool]
		restrictive bool
	}{
		{"disabled", controls.Enabled, false}, {"paused", controls.Paused, true},
		{"stop", controls.Stop, true}, {"completed", controls.Completed, true},
		{"awaiting_user", controls.AwaitingUser, true}, {"refused", controls.Refused, true},
		{"account_restricted", controls.AccountRestricted, true}, {"unknown_outcome", controls.UnknownOutcome, true},
		{"superseded", controls.Superseded, true}, {"pending_transfer", controls.PendingTransfer, true},
	}
	for _, f := range flags {
		if f.field.State == Supplied && f.field.Value == f.restrictive {
			r.Restrictions = append(r.Restrictions, f.name)
		}
	}
	if len(r.Restrictions) > 0 {
		r.Recommendation = "inspect_restrictions"
	}
	if !valid(s, o, c) {
		add("invalid_input")
		return r
	}
	if o.Profile != BBScript {
		add("unsupported_profile")
		return r
	}
	r.Selection, r.SuppliedNow, r.SuppliedMaxAge = s, c.Now, c.MaxAge
	r.Facts = Facts{o.Identity, o.RetrievedAt, o.Enabled, o.Definition, o.Occurrence, o.LoopBinding, o.Owner, o.History, o.LastRunAt, o.NextRunAt, o.LastResult, c.Relationship, c.Controls}
	r.References = append([]string(nil), o.References...)
	for _, cap := range o.Capabilities {
		r.CapabilityClaims = append(r.CapabilityClaims, CapabilityClaim{cap.Kind, append([]string(nil), cap.References...)})
	}
	assess := func(name string, state State) {
		if state == Absent {
			state = Unknown
		}
		r.Assessability[name] = state
		if state != Supplied {
			add(name + "_" + string(state))
		}
	}
	assess("selection", s.Key.State)
	assess("identity", o.Identity.State)
	if s.Key.State != Supplied {
		r.Recommendation = "pin_already_authorized_source"
	}
	if s.Key.State == Supplied && o.Identity.State == Supplied && s.Key.Value != o.Identity.Value {
		r.Assessability["identity"] = Contradictory
		add("identity_mismatch")
	}
	assess("retrieval", o.RetrievedAt.State)
	if o.RetrievedAt.State == Supplied {
		switch {
		case o.RetrievedAt.Value.After(c.Now):
			r.Assessability["retrieval"] = Contradictory
			add("retrieval_future")
		case c.Now.Sub(o.RetrievedAt.Value) > c.MaxAge:
			r.Assessability["retrieval"] = Unknown
			add("retrieval_expired")
		default:
			add("retrieval_recent_relative_to_caller")
		}
	}
	assess("enabled", o.Enabled.State)
	if o.Enabled.State == Supplied && !o.Enabled.Value {
		add("reported_disabled")
	}
	assess("definition", o.Definition.State)
	assess("owner", o.Owner.State)
	assess("occurrence", o.Occurrence.State)
	assess("history", o.History.State)
	assess("last_run", o.LastRunAt.State)
	assess("next_run", o.NextRunAt.State)
	assess("result", o.LastResult.State)
	assess("loop_binding", o.LoopBinding.State)
	assess("relationship", c.Relationship.State)
	assess("controls", c.Controls.State)
	for _, f := range flags {
		if f.field.State != Supplied || c.Controls.State != Supplied {
			if c.Controls.State == Contradictory || f.field.State == Contradictory {
				r.Assessability["controls"] = Contradictory
			} else if r.Assessability["controls"] != Contradictory {
				r.Assessability["controls"] = Unknown
			}
			add("control_" + f.name + "_unavailable")
		}
	}
	if len(r.Restrictions) > 0 {
		r.Recommendation = "inspect_restrictions"
	}
	ownerRevision := Field[string]{}
	if o.Owner.State == Supplied {
		ownerRevision = o.Owner.Value.Revision
		assess("owner_revision", ownerRevision.State)
		if c.Relationship.State == Supplied && ((c.Relationship.Value == Owned && o.Owner.Value.Mode == External) || (c.Relationship.Value == ManageExternal && o.Owner.Value.Mode == Looptender)) {
			r.Assessability["owner"] = Contradictory
			add("owner_relationship_contradiction")
		}
	} else {
		add("owner_revision_unavailable")
	}
	compare := func(name string, expected, observed Field[string]) {
		assess(name, expected.State)
		if expected.State == Supplied && observed.State != Supplied {
			r.Assessability[name] = Unknown
			add(name + "_observation_unavailable")
		} else if expected.State == Supplied && expected.Value != observed.Value {
			r.Assessability[name] = Contradictory
			add(name + "_mismatch")
		}
	}
	compare("expected_definition", s.ExpectedDefinition, o.Definition)
	compare("expected_owner_revision", s.ExpectedOwnerRevision, ownerRevision)
	if o.LastRunAt.State == Supplied && (o.LastRunAt.Value.After(c.Now) || (o.RetrievedAt.State == Supplied && o.LastRunAt.Value.After(o.RetrievedAt.Value))) {
		r.Assessability["last_run"] = Contradictory
		add("last_run_chronology_contradiction")
	}
	if o.History.State == Supplied && o.History.Value == NoHistory && (o.LastRunAt.State == Supplied || o.LastResult.State == Supplied || o.Occurrence.State == Supplied) {
		r.Assessability["history"] = Contradictory
		add("history_contradiction")
	}
	if len(o.Capabilities) > 0 {
		r.Assessability["capabilities"] = Supplied
	} else {
		add("capabilities_unknown")
	}
	return r
}

func fieldValid[T comparable](f Field[T], value func(T) bool) bool {
	var zero T
	switch f.State {
	case Absent, Unknown, Contradictory:
		return f.Value == zero
	case Supplied:
		return value(f.Value)
	default:
		return false
	}
}
func opaque(s string) bool {
	if len(s) == 0 || len(s) > MaxStringBytes {
		return false
	}
	for _, b := range []byte(s) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func key(k Key) bool {
	return opaque(k.SourceInstance) && opaque(k.Device) && opaque(k.User) && opaque(k.Project) && opaque(k.NativeID)
}
func refs(v []string) bool {
	if len(v) > MaxReferences {
		return false
	}
	for _, s := range v {
		if !opaque(s) {
			return false
		}
	}
	return true
}
func valid(s Selection, o Observation, c Context) bool {
	str := func(f Field[string]) bool { return fieldValid(f, opaque) }
	boolean := func(v bool) bool { return true }
	stamp := func(f Field[time.Time]) bool {
		return fieldValid(f, func(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 })
	}
	if c.Now.IsZero() || c.Now.Year() < 1 || c.Now.Year() > 9999 || c.MaxAge < time.Second || c.MaxAge > MaxFreshness {
		return false
	}
	if c.Intensity != "" && c.Intensity != "low" && c.Intensity != "medium" && c.Intensity != "high" && c.Intensity != "unleashed" {
		return false
	}
	if !label(o.Label) || !refs(o.References) || len(o.Capabilities) > MaxCapabilities {
		return false
	}
	if !fieldValid(s.Key, key) || !fieldValid(o.Identity, key) || !str(s.ExpectedDefinition) || !str(s.ExpectedOwnerRevision) || !str(o.Definition) || !str(o.Occurrence) || !str(o.LoopBinding) {
		return false
	}
	if !stamp(o.RetrievedAt) || !stamp(o.LastRunAt) || !stamp(o.NextRunAt) || !fieldValid(o.Enabled, boolean) {
		return false
	}
	if !fieldValid(o.History, func(v History) bool { return v == NoHistory || v == ReportedHistory }) || !fieldValid(o.LastResult, func(v Result) bool { return v == Succeeded || v == Failed || v == Skipped || v == Running }) {
		return false
	}
	if !fieldValid(o.Owner, func(v OwnerClaim) bool { return (v.Mode == External || v.Mode == Looptender) && str(v.Revision) }) {
		return false
	}
	if !fieldValid(c.Relationship, func(v Relationship) bool { return v == ObserveOnly || v == ManageExternal || v == Owned }) {
		return false
	}
	if !fieldValid(c.Controls, func(v Restrictions) bool {
		for _, f := range []Field[bool]{v.Enabled, v.Paused, v.Stop, v.Completed, v.AwaitingUser, v.Refused, v.AccountRestricted, v.UnknownOutcome, v.Superseded, v.PendingTransfer} {
			if !fieldValid(f, boolean) {
				return false
			}
		}
		return true
	}) {
		return false
	}
	seen := map[Capability]bool{}
	for _, cap := range o.Capabilities {
		if !refs(cap.References) || seen[cap.Kind] {
			return false
		}
		seen[cap.Kind] = true
		switch cap.Kind {
		case Inspect, Enable, Disable, RunNow, Cancel, Transfer:
		default:
			return false
		}
	}
	return true
}

func label(s string) bool {
	if len(s) > MaxStringBytes {
		return false
	}
	for _, b := range []byte(s) {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}
