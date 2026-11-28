package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

const renewalTicketFile = "client-completion-renewal.json"

var renewalTicketID = regexp.MustCompile(`^renewal_[0-9a-f]{32}$`)
var errRenewal = errors.New("completion renewal evidence unavailable")

type renewalRequest struct {
	Version    int    `json:"version"`
	RequestID  string `json:"requestId"`
	Effect     string `json:"effect"`
	Historical struct {
		Class       string `json:"class"`
		Independent bool   `json:"acknowledgeIndependentCurrentScope"`
	} `json:"historical"`
	Scope struct {
		ID       string   `json:"id"`
		Revision uint64   `json:"revision"`
		Summary  string   `json:"summary"`
		Refs     []string `json:"refs"`
	} `json:"currentScope"`
	Acknowledge struct {
		Only    bool `json:"onlyCompletion"`
		Wake    bool `json:"laterChecksMayWake"`
		History bool `json:"historicalEvidenceNotAcceptance"`
	} `json:"acknowledge"`
}
type renewalTicket struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	Operation     string    `json:"operation"`
	Binding       string    `json:"binding"`
	RequestDigest string    `json:"requestDigest"`
	ReviewDigest  string    `json:"reviewDigest"`
	IssuedAt      time.Time `json:"issuedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Consumed      bool      `json:"consumed"`
}
type renewalReview struct {
	HistoricalClass    string `json:"historicalClass"`
	CompletionPresent  bool   `json:"completionPresent"`
	OwnerPresent       bool   `json:"ownerPresent"`
	CurrentGoal        string `json:"currentGoal"`
	GoalRelationship   string `json:"goalRelationship"`
	Coverage           string `json:"coverage"`
	Effect             string `json:"effect"`
	LaterChecksMayWake bool   `json:"laterChecksMayWake"`
	// Only with explicit --private. Scope and history are independent sections.
	Scope      *renewalRequest `json:"privateCurrentScope,omitempty"`
	Historical *control.File   `json:"privateHistoricalControl,omitempty"`
}
type renewalReport struct {
	Version        int            `json:"version"`
	RegistrationID string         `json:"registrationId"`
	Outcome        string         `json:"outcome"`
	ControlSave    string         `json:"controlSave"`
	Issues         []string       `json:"issues"`
	Ticket         string         `json:"ticket,omitempty"`
	ReviewDigest   string         `json:"reviewDigest,omitempty"`
	ExpiresAt      *time.Time     `json:"expiresAt,omitempty"`
	Review         *renewalReview `json:"review,omitempty"`
}

func renewalText(s string, max int) bool { return strings.TrimSpace(s) != "" && len(s) <= max }
func validRenewalRequest(r renewalRequest) bool {
	if r.Version != 1 || !renewalText(r.RequestID, 128) || r.Effect != "withdraw_exact_completion" || (r.Historical.Class != "legacy_unbound" && r.Historical.Class != "bound") || !r.Historical.Independent || !r.Acknowledge.Only || !r.Acknowledge.Wake || !r.Acknowledge.History || !renewalText(r.Scope.ID, 128) || r.Scope.Revision == 0 || !renewalText(r.Scope.Summary, 1024) || len(r.Scope.Refs) == 0 || len(r.Scope.Refs) > 32 {
		return false
	}
	for _, ref := range r.Scope.Refs {
		if !renewalText(ref, 512) {
			return false
		}
	}
	return true
}

// Strict exact-tag validation reuses freeze's shape validator for protocols
// without maps. State includes string maps; the small walker below adds only
// those maps, retaining exact tags and duplicate rejection at every level.
func renewalShape(d *json.Decoder, typ reflect.Type, depth int) bool {
	if depth > 64 {
		return false
	}
	tok, err := d.Token()
	if err != nil {
		return false
	}
	if tok == nil {
		return typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	delim, container := tok.(json.Delim)
	if !container {
		return typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{})
	}
	switch delim {
	case '{':
		fields := map[string]reflect.Type{}
		if typ.Kind() == reflect.Struct && typ != reflect.TypeOf(time.Time{}) {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if f.PkgPath == "" && name != "" && name != "-" {
					fields[name] = f.Type
				}
			}
		} else if typ.Kind() != reflect.Map || typ.Key().Kind() != reflect.String {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			name, ok := k.(string)
			if e != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
			field, ok := fields[name]
			if typ.Kind() == reflect.Map {
				field = typ.Elem()
				ok = true
			}
			if !ok || !renewalShape(d, field, depth+1) {
				return false
			}
		}
	case '[':
		if typ.Kind() != reflect.Slice {
			return false
		}
		for d.More() {
			if !renewalShape(d, typ.Elem(), depth+1) {
				return false
			}
		}
	default:
		return false
	}
	_, err = d.Token()
	return err == nil
}
func renewalDecode(raw []byte, dst any, maps bool) error {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return errRenewal
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return errRenewal
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	typ := reflect.TypeOf(dst).Elem()
	ok := false
	if maps {
		ok = renewalShape(d, typ, 0)
	} else {
		ok = freezeExactJSON(d, typ, 0)
	}
	if !ok || d.Decode(new(any)) != io.EOF {
		return errRenewal
	}
	return nil
}

// No-follow + nonblocking descriptor acquisition before fstat. No arbitrary I/O
// deadline or hostile-writer isolation is implied. Unsupported platforms never
// reach this helper through the command.
func renewalRead(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit || before.Mode().Perm()&0077 != 0 {
		return nil, errRenewal
	}
	f, err := openFreezeRecord(path)
	if err != nil {
		return nil, errRenewal
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > limit {
		return nil, errRenewal
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errRenewal
	}
	return raw, nil
}
func validateRenewalControl(raw []byte) error {
	var f control.File
	if renewalDecode(raw, &f, false) != nil {
		return errRenewal
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"completion", "ownership", "openQuestion"} {
		if v, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errRenewal
		}
	}
	canonical, err := json.Marshal(f)
	var roundtrip control.File
	if err != nil || json.Unmarshal(canonical, &roundtrip) != nil || !reflect.DeepEqual(f, roundtrip) {
		return errRenewal
	}
	if f.UpdatedAt.IsZero() {
		return errRenewal
	}
	_, err = control.RenewCompletion(f)
	return err
}
func renewalHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return freezeDigest(b)
}

func renewalBinding(entry registry.Entry, cfg *config.Config, caller string, req renewalRequest) (string, control.File, renewalReview, error) {
	var ctl control.File
	var st state.File
	var g *control.Goal
	review := renewalReview{Coverage: "coverage_not_assessed", Effect: "only_completed_false", LaterChecksMayWake: true}
	raw, err := renewalRead(filepath.Join(entry.StateDir, control.FileName), freezeWireLimit)
	if err != nil || validateRenewalControl(raw) != nil || renewalDecode(raw, &ctl, false) != nil || control.CheckOwner(ctl, caller, false) != nil || checkTransferSources(cfg, false) != nil {
		return "", ctl, review, errRenewal
	}
	raw, err = renewalRead(filepath.Join(entry.StateDir, state.FileName), 1<<20)
	if err != nil || renewalDecode(raw, &st, true) != nil {
		return "", ctl, review, errRenewal
	}
	agent := cfg.Loop.Agent
	if st.Target() != state.Primary {
		routes := effectiveRoutes(cfg, st)
		if routes == nil {
			return "", ctl, review, errRenewal
		}
		route, ok := routes.Find(st.Target())
		if !ok || route.Disabled {
			return "", ctl, review, errRenewal
		}
		agent, ok = st.CandidateAgent(route)
		if !ok {
			return "", ctl, review, errRenewal
		}
	}
	thread := ""
	if agent.Adapter == "bb" && agent.BB != nil {
		thread = agent.BB.Thread
		if !transferThreadID.MatchString(thread) {
			return "", ctl, review, errRenewal
		}
	}
	if control.CheckHoldClearer(control.HoldClearer{Caller: caller, TargetThread: thread, Supervised: supervisedBy(entry.StateDir)}) != nil {
		return "", ctl, review, control.ErrSupervisedSession
	}
	goalRaw, goalErr := renewalRead(filepath.Join(entry.StateDir, control.GoalName), 1<<20)
	if goalErr != nil && !errors.Is(goalErr, os.ErrNotExist) {
		return "", ctl, review, errRenewal
	}
	if goalErr == nil {
		var parsed control.Goal
		if renewalDecode(goalRaw, &parsed, false) != nil {
			return "", ctl, review, errRenewal
		}
		g, err = control.LoadGoal(entry.StateDir)
		if err != nil || g == nil || g.OwningLoop != entry.Loop || !reflect.DeepEqual(parsed, *g) {
			return "", ctl, review, errRenewal
		}
	}
	// Renewal never reads archive contents: only verified absence is eligible.
	// In particular, opening a FIFO here would block with all three locks held.
	_, aerr := os.Lstat(filepath.Join(entry.StateDir, control.ArchiveFileName))
	if !errors.Is(aerr, os.ErrNotExist) {
		return "", ctl, review, errRenewal
	}
	class, err := control.CompletionRenewalClass(ctl)
	if err != nil || class != req.Historical.Class || cfg.SourcesDigest() == "" {
		return "", ctl, review, errRenewal
	}
	review.HistoricalClass = class
	review.CompletionPresent = ctl.Completion != nil
	review.OwnerPresent = ctl.Ownership != nil
	review.CurrentGoal = "absent"
	var summary *control.GoalSummary
	if g != nil {
		s := g.Summary()
		summary = &s
		review.CurrentGoal = "present"
	}
	review.GoalRelationship = ctl.Completion.CompareGoal(summary)
	binding := renewalHash(struct {
		Entry           registry.Entry
		Sources, Caller string
		Control         control.File
		State           state.File
		Goal            *control.Goal
		Request         renewalRequest
	}{entry, cfg.SourcesDigest(), caller, ctl, st, g, req})
	return binding, ctl, review, nil
}

func loadRenewalTicket(dir string) (renewalTicket, error) {
	var t renewalTicket
	if registry.CheckDirectory(dir) != nil {
		return t, errRenewal
	}
	raw, err := renewalRead(filepath.Join(dir, renewalTicketFile), 4096)
	if err != nil {
		return t, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if len(fields) != 9 {
		return t, errRenewal
	}
	if renewalDecode(raw, &t, false) != nil || t.Version != 1 || !renewalTicketID.MatchString(t.ID) || t.Operation != "completion_renewal" || !clientBindingHash.MatchString(t.Binding) || !clientBindingHash.MatchString(t.RequestDigest) || !clientBindingHash.MatchString(t.ReviewDigest) || t.IssuedAt.IsZero() || t.ExpiresAt.Sub(t.IssuedAt) != clientResumeTTL {
		return t, errRenewal
	}
	return t, nil
}

// Close/rename publication supports the tested process-crash boundary, NOT OS
// crash or power-loss durability. No automatic recovery follows an uncertain save.
func saveRenewalTicket(dir string, t renewalTicket) error {
	if _, err := loadRenewalTicket(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errRenewal
	}
	b, err := json.Marshal(t)
	if err != nil {
		return errRenewal
	}
	f, err := os.CreateTemp(dir, ".completion-renewal-*")
	if err != nil {
		return errRenewal
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || os.Rename(f.Name(), filepath.Join(dir, renewalTicketFile)) != nil {
		return errRenewal
	}
	return nil
}

// Fault seams are nil in production; they exercise each acknowledgement boundary
// without touching a real registration or invoking an adapter.
type renewalFaults struct {
	AfterConsume func() error
	BeforeSave   func() error
	AfterSave    func() error
	SaveTicket   func(string, renewalTicket) error
}

func completionRenewal(ctx context.Context, directory, id, op, requestPath, ticket, expect, caller string, private bool, now func() time.Time, fault renewalFaults) (renewalReport, int) {
	r := renewalReport{Version: 1, RegistrationID: id, Outcome: "refused", ControlSave: "not_attempted", Issues: []string{}}
	fail := func(code string) error { r.Issues = append(r.Issues, code); return errRenewal }
	if !freezeSupported() {
		_ = fail("unsupported_platform")
		return r, exitFailed
	}
	if op != "prepare-completion-renewal" && op != "apply-completion-renewal" {
		return r, exitUsage
	}
	raw, err := renewalRead(requestPath, 32768)
	var req renewalRequest
	if err != nil || renewalDecode(raw, &req, false) != nil || !validRenewalRequest(req) {
		_ = fail("request_unavailable")
		return r, exitFailed
	}
	requestDigest := renewalHash(req)
	dir, err := registryDirectory(directory)
	if err != nil {
		_ = fail("registry_unavailable")
		return r, exitFailed
	}
	saveTicket := saveRenewalTicket
	if fault.SaveTicket != nil {
		saveTicket = fault.SaveTicket
	}
	err = registry.WithEntry(dir, id, func(entry registry.Entry) error {
		cfg, ok := clientConfig(entry)
		if !ok {
			return fail("binding_unavailable")
		}
		tick, err := lock.Acquire(entry.StateDir)
		if err != nil {
			return err
		}
		defer tick.Release()
		binding := func() (string, control.File, renewalReview, error) {
			if ctx.Err() != nil {
				return "", control.File{}, renewalReview{}, errRenewal
			}
			current, ok := clientEntry(dir, id)
			if !ok || current != entry {
				return "", control.File{}, renewalReview{}, errRenewal
			}

			if _, ok := clientConfig(entry); !ok || cfg.CheckSources() != nil {
				return "", control.File{}, renewalReview{}, errRenewal
			}
			b, e := renewalRead(requestPath, 32768)
			var fresh renewalRequest
			if e != nil || renewalDecode(b, &fresh, false) != nil || renewalHash(fresh) != requestDigest {
				return "", control.File{}, renewalReview{}, errRenewal
			}
			return renewalBinding(entry, cfg, caller, req)
		}

		if op == "prepare-completion-renewal" {
			held, err := lock.AcquireNamed(entry.StateDir, lock.Control)
			if err != nil {
				return err
			}
			defer held.Release()
			before, ctl, review, err := binding()
			if errors.Is(err, control.ErrSupervisedSession) {
				return fail("supervised_session")
			}
			if err != nil {
				return fail("binding_or_conflict_unavailable")
			}
			after, _, _, err := binding()
			if err != nil || before != after {
				return fail("observation_changed")
			}
			reviewDigest := renewalHash(struct {
				Binding string
				Review  renewalReview
			}{before, review})
			var random [16]byte
			if _, err := rand.Read(random[:]); err != nil {
				return fail("ticket_unavailable")
			}
			at := now().UTC()
			t := renewalTicket{1, "renewal_" + hex.EncodeToString(random[:]), "completion_renewal", before, requestDigest, reviewDigest, at, at.Add(clientResumeTTL), false}
			if saveTicket(entry.StateDir, t) != nil {
				return fail("ticket_publication_unknown")
			}
			r.Outcome = "prepared"
			r.Ticket = t.ID
			r.ReviewDigest = reviewDigest
			r.ExpiresAt = &t.ExpiresAt
			if private {
				review.Scope = &req
				review.Historical = &ctl
			}
			r.Review = &review
			return nil
		}
		_, err = updateControlFromAnnotated(entry.StateDir, "completion_renewal", "", func(f *control.File) error {
			current, checked, _, err := binding()
			if err != nil || !reflect.DeepEqual(checked, *f) {
				return fail("observation_changed")
			}
			t, err := loadRenewalTicket(entry.StateDir)
			at := now().UTC()
			if err != nil || t.ID != ticket || t.Consumed || at.Before(t.IssuedAt) || !at.Before(t.ExpiresAt) || expect != t.ReviewDigest || !clientBindingHash.MatchString(expect) || current != t.Binding || requestDigest != t.RequestDigest {
				return fail("ticket_or_observation_changed")
			}
			t.Consumed = true
			if saveTicket(entry.StateDir, t) != nil {
				r.Outcome = "consumption_unknown"
				return fail("ticket_consumption_unknown")
			}
			r.Outcome = "consumed_unconfirmed"
			if fault.AfterConsume != nil {
				if err := fault.AfterConsume(); err != nil {
					return fail("interrupted_after_consumption")
				}
			}
			again, _, _, err := binding()
			at = now().UTC()
			if err != nil || again != current || at.Before(t.IssuedAt) || !at.Before(t.ExpiresAt) {
				return fail("observation_changed")
			}
			next, err := control.RenewCompletion(*f)
			if err != nil {
				return fail("conflicting_hold")
			}
			// Defend the promised effect even if the pure helper is changed later.
			restored := next
			restored.Completed = f.Completed
			if !reflect.DeepEqual(restored, *f) {
				return fail("unsupported_effect")
			}
			if fault.BeforeSave != nil {
				if err := fault.BeforeSave(); err != nil {
					return fail("interrupted_before_save")
				}
			}
			*f = next
			r.ControlSave = "unconfirmed"
			r.Outcome = "control_save_unknown"
			return nil
		}, func(_ control.File, c *incident.ControlChange) { c.Caller = "terminal" })
		if err != nil && !errors.Is(err, errControlHistory) {
			return err
		}
		r.ControlSave = "confirmed"
		r.Outcome = "completion_withdrawn"
		if errors.Is(err, errControlHistory) {
			r.Outcome = "completion_withdrawn_with_history_gap"
			_ = fail("incident_history_unavailable")
		}
		if state.Append(entry.StateDir, state.Event{Loop: entry.Loop, Event: "control", Action: "completion_renewal", Reason: "completion withdrawn"}) != nil {
			r.Outcome = "completion_withdrawn_with_history_gap"
			_ = fail("run_history_unavailable")
		}
		if fault.AfterSave != nil {
			if err := fault.AfterSave(); err != nil {
				r.Outcome = "outcome_unknown"
				r.ControlSave = "unconfirmed"
				return fail("acknowledgement_unavailable")
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, lock.ErrHeld) {
			_ = fail("busy")
			return r, exitSkipped
		}
		if !errors.Is(err, errRenewal) {
			_ = fail("control_or_registration_unavailable")
		}
		return r, exitFailed
	}
	if len(r.Issues) > 0 {
		return r, exitFailed
	}
	return r, exitOK
}

func completionRenewalCommand(e env, args []string) int {
	fs := flag.NewFlagSet("client "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "registration ID")
	dir := fs.String("directory", "", "private registry directory")
	path := fs.String("request-file", "", "private reviewed request JSON")
	ticket := fs.String("ticket", "", "single-use renewal ticket")
	expect := fs.String("expect-review", "", "prepared review digest")
	private := fs.Bool("private", false, "include private scope and historical control in preparation")
	asJSON := fs.Bool("json", false, "print structured result")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(e.stdout)
			fs.PrintDefaults()
			return exitOK
		}
		e.errf("client: invalid completion renewal arguments")
		return exitUsage
	}
	prepare := args[0] == "prepare-completion-renewal"
	if fs.NArg() != 0 || !clientRegistrationID.MatchString(*id) || !filepath.IsAbs(*path) || (prepare && (*ticket != "" || *expect != "")) || (!prepare && (!renewalTicketID.MatchString(*ticket) || !clientBindingHash.MatchString(*expect) || *private)) {
		e.errf("client: use an absolute --request-file and --id; apply also requires --ticket and --expect-review; --private is preparation only")
		return exitUsage
	}
	r, code := completionRenewal(e.ctx, *dir, *id, args[0], *path, *ticket, *expect, os.Getenv("BB_THREAD_ID"), *private, time.Now, renewalFaults{})
	if *asJSON {
		if json.NewEncoder(e.stdout).Encode(r) != nil {
			return exitFailed
		}
	} else {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Fprintln(e.stdout, string(b))
	}
	return code
}
