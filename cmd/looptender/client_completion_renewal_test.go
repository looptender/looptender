package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

type renewalFixture struct {
	dir     string
	entry   registry.Entry
	request string
	at      time.Time
}

func newRenewalFixture(t *testing.T) renewalFixture {
	t.Helper()
	if !freezeSupported() {
		t.Skip("renewal supported on Darwin/Linux only")
	}
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")
	dir, entry := clientFixture(t)
	at := time.Now().UTC()
	if err := control.Save(entry.StateDir, control.File{Enabled: true, Completed: true, Note: "PRIVATE historical note", OpenQuestion: &control.OpenQuestion{Note: "PRIVATE question", At: at}}); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(entry.StateDir, state.File{}); err != nil {
		t.Fatal(err)
	}
	var req renewalRequest
	req.Version = 1
	req.RequestID = "request_example"
	req.Effect = "withdraw_exact_completion"
	req.Historical.Class = "legacy_unbound"
	req.Historical.Independent = true
	req.Scope.ID = "scope_example"
	req.Scope.Revision = 1
	req.Scope.Summary = "PRIVATE current independent scope"
	req.Scope.Refs = []string{"scope:example"}
	req.Acknowledge.Only = true
	req.Acknowledge.Wake = true
	req.Acknowledge.History = true
	path := filepath.Join(t.TempDir(), "request.json")
	raw, _ := json.Marshal(req)
	resumeWrite(t, path, raw)
	return renewalFixture{dir, entry, path, at}
}
func (f renewalFixture) call(op, ticket, expect string, fault renewalFaults) (renewalReport, int) {
	return completionRenewal(context.Background(), f.dir, f.entry.ID, op, f.request, ticket, expect, os.Getenv("BB_THREAD_ID"), false, func() time.Time { return f.at }, fault)
}
func (f renewalFixture) prepare(t *testing.T) renewalReport {
	t.Helper()
	r, c := f.call("prepare-completion-renewal", "", "", renewalFaults{})
	if c != 0 || r.Ticket == "" || r.ReviewDigest == "" {
		t.Fatal(c, r)
	}
	return r
}
func (f renewalFixture) update(t *testing.T, change func(*control.File)) {
	t.Helper()
	c, e := control.Load(f.entry.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	change(&c)
	if e = control.Save(f.entry.StateDir, c); e != nil {
		t.Fatal(e)
	}
}
func (f renewalFixture) class(t *testing.T, class string) {
	t.Helper()
	raw := resumeBytes(t, f.request)
	var req renewalRequest
	_ = json.Unmarshal(raw, &req)
	req.Historical.Class = class
	raw, _ = json.Marshal(req)
	resumeWrite(t, f.request, raw)
}

func TestCompletionRenewalLegacyAndHistoricalBindings(t *testing.T) {
	for _, kind := range []string{"absent", "unbound_current_goal", "bound_no_goal", "bound_different_goal", "bound_old_revision", "owner_present"} {
		t.Run(kind, func(t *testing.T) {
			f := newRenewalFixture(t)
			if kind != "absent" && kind != "owner_present" {
				f.update(t, func(c *control.File) {
					c.Completion = &control.Completion{DeclaredAt: f.at}
					if kind != "unbound_current_goal" {
						c.Completion.GoalID = "goal_0123456789abcdef0123456789abcdef"
						c.Completion.GoalRevision = 1
					}
				})
				if kind != "unbound_current_goal" {
					f.class(t, "bound")
				}
			}
			if kind == "owner_present" {
				f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 3} })
			}
			if kind == "unbound_current_goal" || kind == "bound_different_goal" || kind == "bound_old_revision" {
				g, e := control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", control.GoalSpec{Title: "fictional", ObjectiveRefs: []string{"objective:example"}, AcceptanceRefs: []string{"acceptance:example"}}, "", 0)
				if e != nil {
					t.Fatal(e)
				}
				if kind == "bound_old_revision" {
					f.update(t, func(c *control.File) { c.Completion.GoalID = g.ID })
					if _, e = control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", g.Revisions[0].Spec, g.ID, 1); e != nil {
						t.Fatal(e)
					}
				}
			}
			before, _ := control.Load(f.entry.StateDir)
			stateBefore := resumeBytes(t, filepath.Join(f.entry.StateDir, state.FileName))
			// Sidecars must remain untouched, even when old own-question evidence exists.
			sentinel := filepath.Join(f.entry.StateDir, "selfhold.json")
			resumeWrite(t, sentinel, []byte("historical-sidecar"))
			p := f.prepare(t)
			if p.Review.Coverage != "coverage_not_assessed" || !p.Review.LaterChecksMayWake {
				t.Fatal(p)
			}
			raw, _ := json.Marshal(p)
			for _, secret := range []string{"PRIVATE", f.entry.ConfigPath, f.entry.StateDir, "scope_example"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("private data escaped", string(raw))
				}
			}
			r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{})
			if c != 0 || r.Outcome != "completion_withdrawn" || r.ControlSave != "confirmed" {
				t.Fatal(c, r)
			}
			after, _ := control.Load(f.entry.StateDir)
			if after.Completed || !after.UpdatedAt.After(before.UpdatedAt) {
				t.Fatal(after)
			}
			after.Completed = true
			after.UpdatedAt = before.UpdatedAt
			if !reflect.DeepEqual(before, after) || !bytes.Equal(stateBefore, resumeBytes(t, filepath.Join(f.entry.StateDir, state.FileName))) || string(resumeBytes(t, sentinel)) != "historical-sidecar" {
				t.Fatal("history or unrelated state changed")
			}
			saved := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
			r, c = f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{})
			if c == 0 || !bytes.Equal(saved, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
				t.Fatal("replay", r)
			}
			j, e := incident.Load(f.entry.StateDir)
			if e != nil || len(j.Records) != 1 || j.Records[0].ControlChange.Operation != "completion_renewal" {
				t.Fatal(j, e)
			}
		})
	}
}

func TestCompletionRenewalStaleEvidence(t *testing.T) {
	for _, change := range []string{"control", "owner_added", "owner_removed", "owner_ABA", "goal_added", "goal_removed", "goal_revision", "state", "target", "config", "request", "after_consumption", "expired", "clock_back", "review", "resume_ticket"} {
		t.Run(change, func(t *testing.T) {
			f := newRenewalFixture(t)
			if change == "owner_removed" || change == "owner_ABA" {
				f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 1} })
			}
			if change == "goal_removed" || change == "goal_revision" {
				_, e := control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", control.GoalSpec{Title: "example", ObjectiveRefs: []string{"objective"}, AcceptanceRefs: []string{"acceptance"}}, "", 0)
				if e != nil {
					t.Fatal(e)
				}
			}
			p := f.prepare(t)
			fault := renewalFaults{}
			switch change {
			case "control":
				f.update(t, func(c *control.File) { c.Note = "changed" })
			case "owner_added":
				f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 1} })
			case "owner_removed":
				f.update(t, func(c *control.File) { c.Ownership = nil })
			case "owner_ABA":
				f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 3} })
			case "goal_added":
				_, e := control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", control.GoalSpec{Title: "example", ObjectiveRefs: []string{"objective"}, AcceptanceRefs: []string{"acceptance"}}, "", 0)
				if e != nil {
					t.Fatal(e)
				}
			case "goal_removed":
				if e := os.Remove(filepath.Join(f.entry.StateDir, control.GoalName)); e != nil {
					t.Fatal(e)
				}
			case "goal_revision":
				g, _ := control.LoadGoal(f.entry.StateDir)
				_, e := control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", g.Revisions[0].Spec, g.ID, 1)
				if e != nil {
					t.Fatal(e)
				}
			case "state", "target":
				s := state.File{}
				if change == "target" {
					s.ActiveTarget = "missing"
				} else {
					s.Nudges = 7
				}
				if e := state.Save(f.entry.StateDir, s); e != nil {
					t.Fatal(e)
				}
			case "config":
				resumeWrite(t, f.entry.ConfigPath, append(resumeBytes(t, f.entry.ConfigPath), []byte("\n# changed\n")...))
			case "request":
				resumeWrite(t, f.request, bytes.Replace(resumeBytes(t, f.request), []byte("scope_example"), []byte("scope_changed"), 1))
			case "after_consumption":
				fault.AfterConsume = func() error { f.update(t, func(c *control.File) { c.Note = "changed after consumption" }); return nil }
			case "expired":
				f.at = f.at.Add(clientResumeTTL)
			case "clock_back":
				f.at = f.at.Add(-time.Second)
			case "review":
				p.ReviewDigest = strings.Repeat("0", 64)
			case "resume_ticket":
				p.Ticket = "resume_" + strings.Repeat("0", 32)
			}
			before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
			r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, fault)
			if c == 0 {
				t.Fatal("accepted stale evidence", r)
			}
			if change != "after_consumption" && !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
				t.Fatal("refusal changed control")
			}
			ctl, _ := control.Load(f.entry.StateDir)
			if !ctl.Completed {
				t.Fatal("cleared after stale observation")
			}
		})
	}
}

func TestCompletionRenewalStrictInputs(t *testing.T) {
	for _, record := range []string{"request", "control", "state", "goal", "ticket"} {
		for _, mutation := range []string{"unknown", "duplicate", "escaped_duplicate", "alias", "null", "malformed", "trailing"} {
			t.Run(record+"/"+mutation, func(t *testing.T) {
				f := newRenewalFixture(t)
				op := "prepare-completion-renewal"
				ticket, review := "", ""
				path := f.request
				switch record {
				case "control":
					path = filepath.Join(f.entry.StateDir, control.FileName)
				case "state":
					path = filepath.Join(f.entry.StateDir, state.FileName)
				case "goal":
					path = filepath.Join(f.entry.StateDir, control.GoalName)
					if _, err := control.WriteGoal(f.entry.StateDir, f.entry.Loop, "", control.GoalSpec{Title: "example", ObjectiveRefs: []string{"objective:example"}, AcceptanceRefs: []string{"acceptance:example"}}, "", 0); err != nil {
						t.Fatal(err)
					}
				case "ticket":
					p := f.prepare(t)
					ticket, review = p.Ticket, p.ReviewDigest
					op = "apply-completion-renewal"
					path = filepath.Join(f.entry.StateDir, renewalTicketFile)
				}
				raw := resumeBytes(t, path)
				var obj map[string]json.RawMessage
				_ = json.Unmarshal(raw, &obj)
				key := ""
				for k := range obj {
					key = k
					break
				}
				switch mutation {
				case "unknown":
					raw = append([]byte(`{"unknown":true,`), raw[1:]...)
				case "duplicate":
					raw = append([]byte(`{"`+key+`":`+string(obj[key])+`,`), raw[1:]...)
				case "escaped_duplicate":
					escaped := fmtRenewalEscaped(key)
					raw = append([]byte(`{"`+escaped+`":`+string(obj[key])+`,`), raw[1:]...)
				case "alias":
					raw = bytes.Replace(raw, []byte(`"`+key+`":`), []byte(`"`+strings.ToUpper(key)+`":`), 1)
				case "null":
					raw = []byte("null")
				case "malformed":
					raw = []byte("{")
				case "trailing":
					raw = append(raw, []byte(" {}")...)
				}
				resumeWrite(t, path, raw)
				before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
				r, c := f.call(op, ticket, review, renewalFaults{})
				if c == 0 || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
					t.Fatal("strict input accepted", r)
				}
			})
		}
	}
}
func fmtRenewalEscaped(s string) string {
	return `\u00` + string("0123456789abcdef"[s[0]>>4]) + string("0123456789abcdef"[s[0]&15]) + s[1:]
}

func TestCompletionRenewalFaultOutcomes(t *testing.T) {
	for _, point := range []string{"consume", "after_consume", "before_save", "control_save", "history", "ack"} {
		t.Run(point, func(t *testing.T) {
			f := newRenewalFixture(t)
			p := f.prepare(t)
			fault := renewalFaults{}
			var held *lock.Lock
			switch point {
			case "consume":
				fault.SaveTicket = func(string, renewalTicket) error { return errors.New("injected") }
			case "after_consume":
				fault.AfterConsume = func() error { return errors.New("injected") }
			case "before_save":
				fault.BeforeSave = func() error { return errors.New("injected") }
			case "control_save":
				if os.Geteuid() == 0 {
					t.Skip("permission failure fixture requires non-root")
				}
				defer os.Chmod(f.entry.StateDir, 0700)
				fault.AfterConsume = func() error { return os.Chmod(f.entry.StateDir, 0500) }
			case "history":
				var e error
				held, e = lock.AcquireNamed(f.entry.StateDir, "incident")
				if e != nil {
					t.Fatal(e)
				}
				defer held.Release()
			case "ack":
				fault.AfterSave = func() error { return errors.New("injected") }
			}
			r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, fault)
			if c == 0 {
				t.Fatal(r)
			}
			ctl, _ := control.Load(f.entry.StateDir)
			if point == "history" || point == "ack" {
				if ctl.Completed {
					t.Fatal("saved effect lost", r)
				}
				if point == "history" && r.Outcome != "completion_withdrawn_with_history_gap" {
					t.Fatal(r)
				}
				if point == "ack" && r.Outcome != "outcome_unknown" {
					t.Fatal(r)
				}
			} else if !ctl.Completed {
				t.Fatal("failed boundary saved control", r)
			}
			if point == "control_save" && (r.Outcome != "control_save_unknown" || r.ControlSave != "unconfirmed") {
				t.Fatal(r)
			}
			if point != "consume" {
				ticket, e := loadRenewalTicket(f.entry.StateDir)
				if e != nil || !ticket.Consumed {
					t.Fatal(ticket, e)
				}
			}
		})
	}
}

func TestCompletionRenewalProcessCrash(t *testing.T) {
	if spec := os.Getenv("LOOPTENDER_TEST_RENEWAL_CRASH"); spec != "" {
		var f renewalFixtureWire
		if json.Unmarshal([]byte(spec), &f) != nil {
			os.Exit(74)
		}
		completionRenewal(context.Background(), f.Directory, f.ID, "apply-completion-renewal", f.Request, f.Ticket, f.Review, "", false, time.Now, renewalFaults{AfterConsume: func() error { os.Exit(73); return nil }})
		os.Exit(75)
	}
	f := newRenewalFixture(t)
	p := f.prepare(t)
	spec, _ := json.Marshal(renewalFixtureWire{f.dir, f.entry.ID, f.request, p.Ticket, p.ReviewDigest})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCompletionRenewalProcessCrash$")
	cmd.Env = append(os.Environ(), "LOOPTENDER_TEST_RENEWAL_CRASH="+string(spec))
	out, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 73 {
		t.Fatal(e, string(out))
	}
	ticket, e := loadRenewalTicket(f.entry.StateDir)
	if e != nil || !ticket.Consumed {
		t.Fatal(ticket, e)
	}
	ctl, _ := control.Load(f.entry.StateDir)
	if !ctl.Completed {
		t.Fatal("crash cleared control")
	}
	if r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{}); c == 0 {
		t.Fatal("replayed consumed crash ticket", r)
	}
}

type renewalFixtureWire struct{ Directory, ID, Request, Ticket, Review string }

func TestCompletionRenewalOrdinaryResumeNegativeControl(t *testing.T) {
	f := newRenewalFixture(t)
	c, _ := control.Load(f.entry.StateDir)
	next, e := control.RenewCompletion(c)
	if e != nil {
		t.Fatal(e)
	}
	clearForResume(&c)
	if reflect.DeepEqual(c, next) || c.Note != "" || c.OpenQuestion != nil || next.Note == "" || next.OpenQuestion == nil {
		t.Fatal("negative control no longer distinguishes broad resume")
	}
}

func TestCompletionRenewalActorAndConflictAdmission(t *testing.T) {
	for _, kind := range []string{"pause", "stop", "await", "episode", "brief", "question", "stop_lanes", "drop", "disabled", "transfer", "superseded", "supervised", "target_caller", "wrong_owner", "null_completion", "null_owner", "null_question_history", "partial_goal", "partial_assignment", "invalid_time", "null_goal", "archive_unknown", "archive_present"} {
		t.Run(kind, func(t *testing.T) {
			f := newRenewalFixture(t)
			switch kind {
			case "supervised":
				t.Setenv(control.SupervisedLoopVar, f.entry.StateDir)
			case "target_caller":
				t.Setenv("BB_THREAD_ID", "thr_example")
			case "wrong_owner":
				f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 1} })
				t.Setenv("BB_THREAD_ID", "thr_other")
			case "null_completion", "null_owner", "null_question_history":
				key := map[string]string{"null_completion": "completion", "null_owner": "ownership", "null_question_history": "openQuestion"}[kind]
				path := filepath.Join(f.entry.StateDir, control.FileName)
				var obj map[string]json.RawMessage
				_ = json.Unmarshal(resumeBytes(t, path), &obj)
				obj[key] = json.RawMessage("null")
				b, _ := json.Marshal(obj)
				resumeWrite(t, path, b)
			case "null_goal":
				resumeWrite(t, filepath.Join(f.entry.StateDir, control.GoalName), []byte("null"))
			case "archive_unknown", "archive_present":
				raw := []byte("{")
				if kind == "archive_present" {
					raw = []byte(`{"version":1}`)
				}
				resumeWrite(t, filepath.Join(f.entry.StateDir, control.ArchiveFileName), raw)
			default:
				f.update(t, func(c *control.File) {
					switch kind {
					case "pause":
						c.Paused = true
					case "stop":
						c.Stop = true
					case "await":
						c.AwaitingUser = true
					case "episode":
						c.EpisodeHold = true
					case "brief":
						c.BriefHold = []control.Brief{{Action: "handoff"}}
					case "question":
						c.Question = &control.Question{}
					case "stop_lanes":
						c.StopLanesAt = &f.at
					case "drop":
						c.StopLanesDropQueued = true
					case "disabled":
						c.Enabled = false
					case "transfer":
						c.PendingTransfer = &control.PendingBBTransfer{}
					case "superseded":
						c.SupersededBy = "thr_other"
					case "partial_goal":
						c.Completion = &control.Completion{DeclaredAt: f.at, GoalRevision: 1}
					case "partial_assignment":
						c.Completion = &control.Completion{DeclaredAt: f.at, AssignmentRevision: 1}
					case "invalid_time":
						c.Completion = &control.Completion{}
					}
				})
			}
			before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
			r, c := f.call("prepare-completion-renewal", "", "", renewalFaults{})
			if c == 0 || r.Ticket != "" || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
				t.Fatal(c, r)
			}
		})
	}
}

func TestCompletionRenewalUnsupportedPlatform(t *testing.T) {
	if freezeSupported() {
		t.Skip("unsupported-platform native fixture")
	}
	// Refuse before opening a request or creating a registry/state directory.
	dir := filepath.Join(t.TempDir(), "not-created")
	r, c := completionRenewal(context.Background(), dir, "loop_example", "prepare-completion-renewal", "missing", "", "", "", false, time.Now, renewalFaults{})
	if c == 0 || len(r.Issues) != 1 || r.Issues[0] != "unsupported_platform" {
		t.Fatal(c, r)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unsupported platform wrote records", err)
	}
}

func TestCompletionRenewalMissingConsumedTicketField(t *testing.T) {
	f := newRenewalFixture(t)
	p := f.prepare(t)
	path := filepath.Join(f.entry.StateDir, renewalTicketFile)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(resumeBytes(t, path), &obj)
	delete(obj, "consumed")
	b, _ := json.Marshal(obj)
	resumeWrite(t, path, b)
	if r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{}); c == 0 {
		t.Fatal("missing replay marker accepted", r)
	}
}

func TestCompletionRenewalCLIAndPrivatePreview(t *testing.T) {
	f := newRenewalFixture(t)
	var out, errs bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errs}
	code := clientCommand(e, []string{"prepare-completion-renewal", "--id", f.entry.ID, "--directory", f.dir, "--request-file", f.request, "--private", "--json"})
	var p renewalReport
	if code != 0 || json.Unmarshal(out.Bytes(), &p) != nil || p.Review == nil || p.Review.Scope == nil || p.Review.Historical == nil {
		t.Fatal(code, out.String(), errs.String())
	}
	out.Reset()
	errs.Reset()
	code = clientCommand(e, []string{"apply-completion-renewal", "--id", f.entry.ID, "--directory", f.dir, "--request-file", f.request, "--ticket", p.Ticket, "--expect-review", p.ReviewDigest, "--json"})
	if code != 0 || !strings.Contains(out.String(), "completion_withdrawn") {
		t.Fatal(code, out.String(), errs.String())
	}
	for _, args := range [][]string{{"prepare-completion-renewal", "--id", f.entry.ID, "--request-file", "relative"}, {"apply-completion-renewal", "--id", f.entry.ID, "--request-file", f.request}, {"prepare-completion-renewal", "--unknown"}} {
		if c := clientCommand(e, args); c != exitUsage {
			t.Fatal(args, c)
		}
	}
	if c := clientCommand(e, []string{"prepare-completion-renewal", "--help"}); c != 0 {
		t.Fatal(c)
	}
}

func TestCompletionRenewalNoContainerLossAndMapAliases(t *testing.T) {
	f := newRenewalFixture(t)
	path := filepath.Join(f.entry.StateDir, control.FileName)
	raw := resumeBytes(t, path)
	raw = append([]byte(`{"briefHold":[],`), raw[1:]...)
	resumeWrite(t, path, raw)
	if r, c := f.call("prepare-completion-renewal", "", "", renewalFaults{}); c == 0 {
		t.Fatal("accepted a representation ordinary Save would collapse", r)
	}
	// State's map keys are data, but duplicate/escaped-equivalent map keys are
	// still ambiguous; the state walker cannot defer this to json.Unmarshal.
	for _, raw := range []string{`{"exactCut":{"example":1,"\u0065xample":2}}`, `{"exactCut":{"example":null}}`, `{"exactCut":{"example":1},"EXACTCUT":{}}`} {
		var st state.File
		if renewalDecode([]byte(raw), &st, true) == nil {
			t.Fatal("ambiguous state map", raw)
		}
	}
	var st state.File
	if renewalDecode([]byte(`{"exactCut":{"EXAMPLE":1,"example":2}}`), &st, true) != nil {
		t.Fatal("legitimate distinct map data rejected")
	}
}

// A new_thread candidate has no configured BB thread: the current supervised
// target is the created identity in the strictly loaded state snapshot.
func renewalCreatedTargetFixture(t *testing.T, owner bool) renewalFixture {
	t.Helper()
	f := newRenewalFixture(t)
	raw := resumeBytes(t, f.entry.ConfigPath)
	resumeWrite(t, f.entry.ConfigPath, append(raw, []byte("  fallbacks: fallbacks.yaml\n")...))
	resumeWrite(t, filepath.Join(filepath.Dir(f.entry.ConfigPath), "fallbacks.yaml"), []byte(`version: 1
candidates:
  - id: alternative
    adapter: bb
    provider: example
    bb: {new_thread: true, cli: nonexistent-fixture}
    probe: {kind: command, command: [nonexistent-probe]}
`))
	st := state.File{ActiveTarget: "alternative", NewThreads: map[string]state.CreatedThread{"alternative": {Thread: "thr_created_target", CreatedAt: f.at}}}
	if err := state.Save(f.entry.StateDir, st); err != nil {
		t.Fatal(err)
	}
	if owner {
		f.update(t, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_created_target", Generation: 2} })
	}
	cfg, ok := clientConfig(f.entry)
	if !ok {
		t.Fatal("invalid created-target fixture")
	}
	a := activeAgent(cfg) // only for the regular-file fixture equivalence assertion
	if a.BB == nil || a.BB.Thread != "thr_created_target" {
		t.Fatal("fixture did not establish created target")
	}
	return f
}

func TestCompletionRenewalCreatedTargetActor(t *testing.T) {
	for _, owned := range []bool{false, true} {
		for _, op := range []string{"prepare-completion-renewal", "apply-completion-renewal"} {
			name := op + "/absent_owner"
			if owned {
				name = op + "/matching_owner"
			}
			t.Run(name, func(t *testing.T) {
				f := renewalCreatedTargetFixture(t, owned)
				ticket, review := "", ""
				if op == "apply-completion-renewal" {
					p := f.prepare(t)
					ticket, review = p.Ticket, p.ReviewDigest
				}
				t.Setenv("BB_THREAD_ID", "thr_created_target")
				before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
				// A configured route lookup is insufficient even when the owner is the
				// caller. Assert the actual guard independently of ticket staleness.
				cfg, _ := clientConfig(f.entry)
				var req renewalRequest
				_ = json.Unmarshal(resumeBytes(t, f.request), &req)
				if _, _, _, err := renewalBinding(f.entry, cfg, "thr_created_target", req); !errors.Is(err, control.ErrSupervisedSession) {
					t.Fatal("created target was not refused by actor guard", err)
				}
				r, c := f.call(op, ticket, review, renewalFaults{})
				if c == 0 || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
					t.Fatal("self renewal", c, r)
				}
				if op == "prepare-completion-renewal" {
					if r.Ticket != "" || len(r.Issues) != 1 || r.Issues[0] != "supervised_session" {
						t.Fatal(r)
					}
				} else {
					saved, err := loadRenewalTicket(f.entry.StateDir)
					if err != nil || saved.Consumed {
						t.Fatal("actor refusal consumed ticket", saved, err)
					}
				}
			})
		}
	}
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "external_absent_owner", true: "external_matching_target_owner"}[owned], func(t *testing.T) {
			f := renewalCreatedTargetFixture(t, owned)
			p := f.prepare(t)
			r, c := f.call("apply-completion-renewal", p.Ticket, p.ReviewDigest, renewalFaults{})
			if c != 0 || r.Outcome != "completion_withdrawn" {
				t.Fatal("external operator refused", c, r)
			}
		})
	}
}

func TestCompletionRenewalCreatedTargetUnknown(t *testing.T) {
	for _, thread := range []string{"", "invalid"} {
		for _, op := range []string{"prepare-completion-renewal", "apply-completion-renewal"} {
			t.Run(op+"/"+thread, func(t *testing.T) {
				f := renewalCreatedTargetFixture(t, false)
				ticket, review := "", ""
				if op == "apply-completion-renewal" {
					p := f.prepare(t)
					ticket, review = p.Ticket, p.ReviewDigest
				}
				st, err := state.Load(f.entry.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				st.NewThreads["alternative"] = state.CreatedThread{Thread: thread, CreatedAt: f.at}
				if err = state.Save(f.entry.StateDir, st); err != nil {
					t.Fatal(err)
				}
				before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
				r, c := f.call(op, ticket, review, renewalFaults{})
				if c == 0 || !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
					t.Fatal("unknown created identity accepted", c, r)
				}
			})
		}
	}
}
