package jobplan

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func supplied[T comparable](v T) Field[T] { return Field[T]{Supplied, v} }
func fixture() (Selection, Observation, Context) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	k := Key{"bb-example", "device-example", "user-example", "project-example", "auto_example"}
	controls := Restrictions{Enabled: supplied(true), Paused: supplied(false), Stop: supplied(false), Completed: supplied(false), AwaitingUser: supplied(false), Refused: supplied(false), AccountRestricted: supplied(false), UnknownOutcome: supplied(false), Superseded: supplied(false), PendingTransfer: supplied(false)}
	return Selection{Key: supplied(k)}, Observation{Profile: BBScript, Identity: supplied(k), Label: "Nightly example", RetrievedAt: supplied(now), Enabled: supplied(true), History: supplied(ReportedHistory), LastRunAt: supplied(now.Add(-time.Hour)), NextRunAt: supplied(now.Add(time.Hour)), LastResult: supplied(Failed), References: []string{"evidence-example"}}, Context{Now: now, MaxAge: 5 * time.Minute, Relationship: supplied(ObserveOnly), Controls: supplied(controls)}
}
func has(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func previewOnly(t *testing.T, r Report) {
	t.Helper()
	if r.Effects != (EffectAvailability{"unavailable", "preview_only"}) || r.Confidence != "caller_supplied_unverified" {
		t.Fatalf("authority escaped: %+v", r)
	}
}
func invalid(t *testing.T, s Selection, o Observation, c Context) {
	t.Helper()
	r := Preview(s, o, c)
	previewOnly(t, r)
	if !has(r.Reasons, "invalid_input") {
		t.Fatalf("not refused: %+v", r)
	}
	if r.Facts.Identity.State != Absent || len(r.References) != 0 {
		t.Fatal("invalid input exposed facts")
	}
}

// 1: The current BB doctor-like shape is useful without supplying authority or
// inventing the revision, ownership, occurrence or loop binding it cannot supply.
func TestSelectedScriptFactsAndUnsupportedProfile(t *testing.T) {
	s, o, c := fixture()
	r := Preview(s, o, c)
	previewOnly(t, r)
	if !r.Facts.Enabled.Value || r.Facts.LastResult.Value != Failed || r.Facts.NextRunAt != o.NextRunAt {
		t.Fatal("lost reported facts")
	}
	for _, name := range []string{"definition", "owner", "owner_revision", "occurrence", "loop_binding"} {
		if r.Assessability[name] != Unknown {
			t.Fatal(name, r)
		}
	}
	if r.Facts.Occurrence.State != Absent || r.Facts.Owner.State != Absent {
		t.Fatal("invented identity")
	}
	for _, p := range []Profile{"bb_agent", "unknown", ""} {
		o.Profile = p
		r = Preview(s, o, c)
		previewOnly(t, r)
		if !has(r.Reasons, "unsupported_profile") || r.Facts.Identity.State != Absent {
			t.Fatal("profile accepted")
		}
	}
}

// 2: Tuple components, case and delimiters remain exact. Display labels never
// repair or merge different source/project/native identities.
func TestExactSelectionIdentityAndAliasSafety(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			s, o, c := fixture()
			v := reflect.ValueOf(&o.Identity.Value).Elem().Field(i)
			v.SetString(v.String() + "-other")
			r := Preview(s, o, c)
			previewOnly(t, r)
			if r.Assessability["identity"] != Contradictory || !has(r.Reasons, "identity_mismatch") {
				t.Fatal(r)
			}
		})
	}
	s, o, c := fixture()
	s.Key.Value.SourceInstance = "a:b"
	s.Key.Value.Device = "c"
	o.Identity.Value.SourceInstance = "a"
	o.Identity.Value.Device = "b:c"
	if Preview(s, o, c).Assessability["identity"] != Contradictory {
		t.Fatal("delimiter collision")
	}
	s, o, c = fixture()
	o.Identity.Value.NativeID = "AUTO_example"
	if Preview(s, o, c).Assessability["identity"] != Contradictory {
		t.Fatal("case folded")
	}
	s, o, c = fixture()
	o.Label = "different display"
	if Preview(s, o, c).Assessability["identity"] != Supplied {
		t.Fatal("label became identity")
	}
	s.Key = Field[Key]{}
	r := Preview(s, o, c)
	if r.Assessability["selection"] != Unknown || r.Recommendation != "pin_already_authorized_source" {
		t.Fatal(r)
	}
}

// 3: Explicit false/none is different from missing; contradictions stay visible.
func TestPresenceAndOwnershipDimensions(t *testing.T) {
	s, o, c := fixture()
	o.Enabled = supplied(false)
	r := Preview(s, o, c)
	if r.Assessability["enabled"] != Supplied || r.Facts.Enabled.Value {
		t.Fatal(r)
	}
	o.Enabled = Field[bool]{}
	if Preview(s, o, c).Assessability["enabled"] != Unknown {
		t.Fatal("missing enabled became false evidence")
	}
	o.History = supplied(NoHistory)
	o.LastRunAt = Field[time.Time]{}
	o.LastResult = Field[Result]{}
	r = Preview(s, o, c)
	if r.Assessability["history"] != Supplied {
		t.Fatal(r)
	}
	o.History = Field[History]{}
	if Preview(s, o, c).Assessability["history"] != Unknown {
		t.Fatal("missing history became none")
	}
	o.History = supplied(NoHistory)
	o.Occurrence = supplied("occ-example")
	if Preview(s, o, c).Assessability["history"] != Contradictory {
		t.Fatal("conflicting history")
	}
	s, o, c = fixture()
	o.Owner = supplied(OwnerClaim{Mode: External})
	if Preview(s, o, c).Assessability["owner"] != Supplied {
		t.Fatal("reported owner lost")
	}
	c.Relationship = supplied(Owned)
	r = Preview(s, o, c)
	if r.Assessability["owner"] != Contradictory {
		t.Fatal("owned/external conflict")
	}
	previewOnly(t, r)
	c.Controls = Field[Restrictions]{}
	if Preview(s, o, c).Assessability["controls"] != Unknown {
		t.Fatal("missing inventory became empty")
	}
	c.Controls = supplied(Restrictions{Enabled: supplied(true)})
	if Preview(s, o, c).Assessability["controls"] != Unknown {
		t.Fatal("partial controls became complete")
	}
	c.Controls = Field[Restrictions]{State: Contradictory}
	if Preview(s, o, c).Assessability["controls"] != Contradictory {
		t.Fatal("contradiction erased")
	}
}

// 4: No acquired time and no due/execution inference. Freshness is only relative
// to supplied time/policy; reported outcome never grants useful-work acceptance.
func TestRetrievalAndReportedTimeBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		age    time.Duration
		state  State
		reason string
	}{{"boundary", 5 * time.Minute, Supplied, "retrieval_recent_relative_to_caller"}, {"expired", 5*time.Minute + time.Nanosecond, Unknown, "retrieval_expired"}, {"future", -time.Nanosecond, Contradictory, "retrieval_future"}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, o, c := fixture()
			o.RetrievedAt = supplied(c.Now.Add(-tt.age))
			r := Preview(s, o, c)
			if r.Assessability["retrieval"] != tt.state || !has(r.Reasons, tt.reason) {
				t.Fatal(r)
			}
			previewOnly(t, r)
		})
	}
	s, o, c := fixture()
	o.RetrievedAt = supplied(time.Time{})
	invalid(t, s, o, c)
	for _, age := range []time.Duration{0, -time.Second, time.Second - time.Nanosecond, MaxFreshness + time.Nanosecond} {
		s, o, c = fixture()
		c.MaxAge = age
		invalid(t, s, o, c)
	}
	s, o, c = fixture()
	c.Now = time.Time{}
	invalid(t, s, o, c)
	s, o, c = fixture()
	c.Now = c.Now.Add(-time.Second)
	if Preview(s, o, c).Assessability["retrieval"] != Contradictory {
		t.Fatal("rollback accepted")
	}
	for _, outcome := range []Result{Succeeded, Failed, Skipped, Running} {
		s, o, c = fixture()
		o.LastResult = supplied(outcome)
		o.LastRunAt = Field[time.Time]{}
		r := Preview(s, o, c)
		previewOnly(t, r)
		if r.Assessability["last_run"] != Unknown || r.Facts.LastRunAt.State != Absent {
			t.Fatal("retrieval supplied history time")
		}
	}
	s, o, c = fixture()
	o.LastRunAt = supplied(c.Now.Add(time.Second))
	if Preview(s, o, c).Assessability["last_run"] != Contradictory {
		t.Fatal("future history accepted")
	}
	s, o, c = fixture()
	r := Preview(s, o, c)
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(r, Preview(s, o, c)) {
			t.Fatal("nondeterminism/self-refresh")
		}
	}
}

// 5: Only explicit expected-vs-observed revisions, never action history or
// generation allocation. Recency changes cannot invent/change native identity.
func TestExplicitExpectedRevisionComparisons(t *testing.T) {
	for _, dimension := range []string{"definition", "owner"} {
		for _, mode := range []string{"match", "changed", "missing", "contradictory"} {
			t.Run(dimension+"/"+mode, func(t *testing.T) {
				s, o, c := fixture()
				f := supplied("rev-example")
				if mode == "changed" {
					f = supplied("rev-other")
				}
				if mode == "missing" {
					f = Field[string]{}
				}
				if mode == "contradictory" {
					f = Field[string]{State: Contradictory}
				}
				key := "expected_definition"
				if dimension == "definition" {
					s.ExpectedDefinition = supplied("rev-example")
					o.Definition = f
				} else {
					s.ExpectedOwnerRevision = supplied("rev-example")
					o.Owner = supplied(OwnerClaim{External, f})
					key = "expected_owner_revision"
				}
				want := Supplied
				if mode == "changed" {
					want = Contradictory
				}
				if mode == "missing" || mode == "contradictory" {
					want = Unknown
				}
				r := Preview(s, o, c)
				if r.Assessability[key] != want {
					t.Fatal(r)
				}
				previewOnly(t, r)
			})
		}
	}
	s, o, c := fixture()
	o.Definition = supplied("rev-example")
	o.Owner = supplied(OwnerClaim{External, supplied("owner-example")})
	a := Preview(s, o, c)
	o.RetrievedAt = supplied(c.Now.Add(-time.Second))
	b := Preview(s, o, c)
	if a.Facts.Definition != b.Facts.Definition || a.Facts.Owner != b.Facts.Owner || b.Facts.Occurrence.State != Absent {
		t.Fatal("time minted identity")
	}
}

// 6: Every independent restriction and the complete set coexist with source
// facts. Neither intensity nor green-evidence references clear anything.
func TestEveryRestrictionSurvives(t *testing.T) {
	names := []string{"disabled", "paused", "stop", "completed", "awaiting_user", "refused", "account_restricted", "unknown_outcome", "superseded", "pending_transfer"}
	for i, name := range names {
		t.Run(name, func(t *testing.T) {
			s, o, c := fixture()
			v := reflect.ValueOf(&c.Controls.Value).Elem().Field(i)
			v.Set(reflect.ValueOf(supplied(i != 0)))
			c.Intensity = "unleashed"
			o.References = []string{"monitoring-claim", "green-commit", "green-ci"}
			r := Preview(s, o, c)
			previewOnly(t, r)
			if !has(r.Restrictions, name) || r.Recommendation != "inspect_restrictions" || r.Facts.Identity != o.Identity {
				t.Fatal(r)
			}
		})
	}
	s, o, c := fixture()
	v := reflect.ValueOf(&c.Controls.Value).Elem()
	for i := 0; i < v.NumField(); i++ {
		v.Field(i).Set(reflect.ValueOf(supplied(i != 0)))
	}
	r := Preview(s, o, c)
	if !reflect.DeepEqual(r.Restrictions, names) {
		t.Fatal(r.Restrictions)
	}
	// A malformed unrelated input must not hide known restrictions.
	o.Label = strings.Repeat("x", MaxStringBytes+1)
	r = Preview(s, o, c)
	if !reflect.DeepEqual(r.Restrictions, names) || !has(r.Reasons, "invalid_input") {
		t.Fatal(r)
	}
}

// 7: Independent of missing-evidence refusals: every optional claim populated
// and every claimed effect supported still cannot create an executable output.
func TestFullyPopulatedClaimsNeverEnableEffects(t *testing.T) {
	s, o, c := fixture()
	s.ExpectedDefinition = supplied("definition-example")
	o.Definition = s.ExpectedDefinition
	s.ExpectedOwnerRevision = supplied("owner-example")
	o.Owner = supplied(OwnerClaim{Looptender, s.ExpectedOwnerRevision})
	o.Occurrence = supplied("occ-example")
	o.LoopBinding = supplied("loop-example")
	c.Relationship = supplied(Owned)
	c.Intensity = "unleashed"
	for _, kind := range []Capability{Inspect, Enable, Disable, RunNow, Cancel, Transfer} {
		o.Capabilities = append(o.Capabilities, CapabilityClaim{kind, []string{"capability-claim"}})
	}
	r := Preview(s, o, c)
	previewOnly(t, r)
	if has(r.Reasons, "invalid_input") || r.Assessability["expected_definition"] != Supplied || r.Assessability["owner"] != Supplied || r.Assessability["capabilities"] != Supplied {
		t.Fatal("fixture not fully populated", r)
	}
	for _, name := range []string{"Ready", "SafeToDispatch", "CanAdopt", "AuthorityVerified"} {
		if _, ok := reflect.TypeOf(r).FieldByName(name); ok {
			t.Fatal("authority field", name)
		}
	}
}

// 8: Implementation caps, invalid states/optional combinations, fixed reasons,
// deep copies and production-import/time-acquisition constraints.
func TestBoundsEnumsAndPrivateDiagnostics(t *testing.T) {
	for i := 0; i < 5; i++ {
		for _, n := range []int{0, MaxStringBytes, MaxStringBytes + 1} {
			s, o, c := fixture()
			v := strings.Repeat("x", n)
			reflect.ValueOf(&s.Key.Value).Elem().Field(i).SetString(v)
			o.Identity = s.Key
			r := Preview(s, o, c)
			if has(r.Reasons, "invalid_input") != (n != MaxStringBytes) {
				t.Fatal("key boundary", i, n, r)
			}
		}
	}
	changes := []func(*Selection, *Observation, *Context){
		func(s *Selection, o *Observation, c *Context) { o.Label = strings.Repeat("x", MaxStringBytes+1) },
		func(s *Selection, o *Observation, c *Context) {
			s.ExpectedDefinition = supplied(strings.Repeat("x", MaxStringBytes+1))
		},
		func(s *Selection, o *Observation, c *Context) {
			s.ExpectedOwnerRevision = supplied(strings.Repeat("x", MaxStringBytes+1))
		},
		func(s *Selection, o *Observation, c *Context) { o.Definition = supplied("") },
		func(s *Selection, o *Observation, c *Context) {
			o.Occurrence = supplied(strings.Repeat("x", MaxStringBytes+1))
		},
		func(s *Selection, o *Observation, c *Context) {
			o.LoopBinding = supplied(strings.Repeat("x", MaxStringBytes+1))
		},
		func(s *Selection, o *Observation, c *Context) {
			o.Owner = supplied(OwnerClaim{External, supplied(strings.Repeat("x", MaxStringBytes+1))})
		},
		func(s *Selection, o *Observation, c *Context) { o.Enabled = Field[bool]{Absent, true} },
		func(s *Selection, o *Observation, c *Context) { o.Enabled.State = "made_up" },
		func(s *Selection, o *Observation, c *Context) { o.History = supplied(History("made_up")) },
		func(s *Selection, o *Observation, c *Context) { o.LastResult = supplied(Result("made_up")) },
		func(s *Selection, o *Observation, c *Context) { o.Owner = supplied(OwnerClaim{Mode: "made_up"}) },
		func(s *Selection, o *Observation, c *Context) { c.Relationship = supplied(Relationship("made_up")) },
		func(s *Selection, o *Observation, c *Context) { c.Intensity = "made_up" },
		func(s *Selection, o *Observation, c *Context) { o.Capabilities = []CapabilityClaim{{Kind: "made_up"}} },
		func(s *Selection, o *Observation, c *Context) { o.References = make([]string, MaxReferences+1) },
		func(s *Selection, o *Observation, c *Context) {
			o.Capabilities = make([]CapabilityClaim, MaxCapabilities+1)
		},
		func(s *Selection, o *Observation, c *Context) {
			o.Capabilities = []CapabilityClaim{{Kind: Inspect}, {Kind: Inspect}}
		},
		func(s *Selection, o *Observation, c *Context) {
			o.Capabilities = []CapabilityClaim{{Inspect, make([]string, MaxReferences+1)}}
		},
	}
	for i, change := range changes {
		t.Run(strconv.Itoa(i), func(t *testing.T) { s, o, c := fixture(); change(&s, &o, &c); invalid(t, s, o, c) })
	}
	for _, where := range []string{"refs", "cap_refs"} {
		for _, n := range []int{0, MaxStringBytes, MaxStringBytes + 1} {
			s, o, c := fixture()
			ref := strings.Repeat("x", n)
			if where == "refs" {
				o.References = []string{ref}
			} else {
				o.Capabilities = []CapabilityClaim{{Inspect, []string{ref}}}
			}
			r := Preview(s, o, c)
			if has(r.Reasons, "invalid_input") != (n != MaxStringBytes) {
				t.Fatal(where, n)
			}
		}
	}
	s, o, c := fixture()
	o.References = make([]string, MaxReferences)
	for i := range o.References {
		o.References[i] = "ref-example"
	}
	if has(Preview(s, o, c).Reasons, "invalid_input") {
		t.Fatal("max refs rejected")
	}
	s, o, c = fixture()
	canary := "PRIVATE_PROMPT_ERROR_SECRET"
	o.Label = canary
	o.References = []string{canary}
	r := Preview(s, o, c)
	if strings.Contains(strings.Join(r.Reasons, " ")+r.Recommendation, canary) {
		t.Fatal("raw diagnostics")
	}
	o.Enabled.State = State(canary)
	r = Preview(s, o, c)
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), canary) {
		t.Fatal("invalid value echoed")
	}
}
func TestNoMutableAliasingOrInputMutation(t *testing.T) {
	s, o, c := fixture()
	o.Capabilities = []CapabilityClaim{{Inspect, []string{"cap-example"}}}
	before, _ := json.Marshal([]any{s, o, c})
	r := Preview(s, o, c)
	after, _ := json.Marshal([]any{s, o, c})
	if string(before) != string(after) {
		t.Fatal("input mutation")
	}
	o.References[0] = "changed-input"
	o.Capabilities[0].References[0] = "changed-input"
	if r.References[0] != "evidence-example" || r.CapabilityClaims[0].References[0] != "cap-example" {
		t.Fatal("input aliased output")
	}
	r.References[0] = "changed-output"
	r.CapabilityClaims[0].References[0] = "changed-output"
	r.CapabilityClaims[0].Kind = Transfer
	r.Assessability["identity"] = Contradictory
	r.Facts.Identity.Value.Project = "changed-output"
	if o.References[0] != "changed-input" || o.Capabilities[0].Kind != Inspect || o.Identity.Value.Project == "changed-output" {
		t.Fatal("output aliased input")
	}
	a := Preview(s, o, c)
	a.Assessability["identity"] = Contradictory
	if Preview(s, o, c).Assessability["identity"] != Supplied {
		t.Fatal("reports share state")
	}
}
func TestProductionHasNoIOOrClockAcquisition(t *testing.T) {
	for _, name := range []string{"types.go", "preview.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value != "\"time\"" {
				t.Fatal("side-effect dependency", imp.Path.Value)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if x, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" {
					switch x.Sel.Name {
					case "Time", "Duration", "Second", "Hour":
					default:
						t.Error("time acquisition/unsupported dependency", x.Sel.Name)
					}
				}
			}
			return true
		})
	}
}

func TestStringAndFreshnessExactCapsAndNoRawContradictions(t *testing.T) {
	// All opaque optional revisions/bindings can reach the fixed cap, but never
	// carry a hidden value when their state declares absence or contradiction.
	setters := []func(*Selection, *Observation, string){
		func(s *Selection, o *Observation, v string) { s.ExpectedDefinition = supplied(v) },
		func(s *Selection, o *Observation, v string) { s.ExpectedOwnerRevision = supplied(v) },
		func(s *Selection, o *Observation, v string) { o.Definition = supplied(v) },
		func(s *Selection, o *Observation, v string) { o.Occurrence = supplied(v) },
		func(s *Selection, o *Observation, v string) { o.LoopBinding = supplied(v) },
		func(s *Selection, o *Observation, v string) { o.Owner = supplied(OwnerClaim{External, supplied(v)}) },
	}
	for i, set := range setters {
		for _, n := range []int{0, MaxStringBytes, MaxStringBytes + 1} {
			s, o, c := fixture()
			set(&s, &o, strings.Repeat("x", n))
			r := Preview(s, o, c)
			if has(r.Reasons, "invalid_input") != (n != MaxStringBytes) {
				t.Fatal("optional cap", i, n)
			}
		}
	}
	for _, n := range []int{0, MaxStringBytes, MaxStringBytes + 1} {
		s, o, c := fixture()
		o.Label = strings.Repeat("x", n)
		if has(Preview(s, o, c).Reasons, "invalid_input") != (n > MaxStringBytes) {
			t.Fatal("label cap", n)
		}
	}
	for _, age := range []time.Duration{time.Second, MaxFreshness} {
		s, o, c := fixture()
		c.MaxAge = age
		if has(Preview(s, o, c).Reasons, "invalid_input") {
			t.Fatal("freshness cap", age)
		}
	}
	s, o, c := fixture()
	o.Capabilities = []CapabilityClaim{{Inspect, make([]string, MaxReferences)}}
	for i := range o.Capabilities[0].References {
		o.Capabilities[0].References[i] = strings.Repeat("x", MaxStringBytes)
	}
	if has(Preview(s, o, c).Reasons, "invalid_input") {
		t.Fatal("cap refs maximum")
	}
	s, o, c = fixture()
	o.Owner = Field[OwnerClaim]{Contradictory, OwnerClaim{Mode: External}}
	invalid(t, s, o, c)
	s, o, c = fixture()
	o.Definition = Field[string]{Unknown, "hidden-value"}
	invalid(t, s, o, c)
	s, o, c = fixture()
	o.Identity.Value.Project = "private\nerror"
	invalid(t, s, o, c)
	s, o, c = fixture()
	c.Controls.Value.Stop = Field[bool]{State: Contradictory}
	if Preview(s, o, c).Assessability["controls"] != Contradictory {
		t.Fatal("contradictory control erased")
	}
	s, o, c = fixture()
	r := Preview(s, o, c)
	if r.Selection != s || r.SuppliedNow != c.Now || r.SuppliedMaxAge != c.MaxAge {
		t.Fatal("lost supplied comparison basis")
	}
	r.Selection.Key.Value.NativeID = "output-only"
	if s.Key.Value.NativeID == "output-only" {
		t.Fatal("selection alias")
	}
}
