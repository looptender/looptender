package prready

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const merge = "cccccccccccccccccccccccccccccccccccccccc"

func ptr[T any](v T) *T { return &v }
func conn[T any](v ...T) Connection[T] {
	if v == nil {
		v = []T{}
	}
	return Connection[T]{TotalCount: ptr(len(v)), PageInfo: &Page{ptr(false)}, Nodes: v}
}
func fixture() Snapshot {
	var s Snapshot
	p := &s.PR
	p.ID = "PR_example"
	p.State = "OPEN"
	p.HeadRefOid = head
	p.BaseRefOid = base
	p.BaseRefName = "main"
	p.UpdatedAt = "2026-01-01T00:00:00Z"
	p.Mergeable = "MERGEABLE"
	p.MergeStateStatus = "CLEAN"
	p.IsDraft = ptr(false)
	p.ReviewDecision = ptr("APPROVED")
	p.PotentialMergeCommit = &struct {
		OID     string
		Parents Connection[OID]
	}{merge, conn(OID{base}, OID{head})}
	p.ReviewThreads = conn[Thread]()
	p.Reviews = conn(Review{ID: "review1", State: "APPROVED", SubmittedAt: "2026-01-01T00:00:00Z", Author: &struct{ Login string }{"example-reviewer"}, Commit: &OID{head}})
	p.Comments = &struct{ TotalCount *int }{ptr(0)}
	p.BaseRef = &struct {
		Target               *OID
		BranchProtectionRule *struct {
			RequiresStatusChecks *bool
			RequiredStatusChecks []struct {
				Context string
				App     *struct{ DatabaseID int64 }
			}
		}
	}{Target: &OID{base}}
	s.HeadChecks = Checks{ptr(1), []Check{{ID: 1, Name: "test", Status: "completed", Conclusion: "success", CompletedAt: "2026-01-01T00:00:00Z", CheckSuite: &struct {
		ID int64 `json:"id"`
	}{1}, HeadSHA: head, App: &struct {
		ID int64 `json:"id"`
	}{10}}}}
	s.MergeChecks = Checks{ptr(1), []Check{{ID: 2, Name: "test", Status: "completed", Conclusion: "success", CompletedAt: "2026-01-01T00:00:00Z", CheckSuite: &struct {
		ID int64 `json:"id"`
	}{1}, HeadSHA: merge, App: &struct {
		ID int64 `json:"id"`
	}{10}}}}
	s.HeadStatuses = Statuses{ptr(0), []json.RawMessage{}}
	s.MergeStatuses = s.HeadStatuses
	s.Rules = []Rule{{Type: "required_status_checks", Parameters: json.RawMessage(`{"required_status_checks":[{"context":"test","integration_id":10}]}`)}}
	return s
}
func TestReadinessRefusesIncompleteAndUnsupportedEvidence(t *testing.T) {
	cases := []struct {
		name, reason string
		edit         func(*Snapshot)
	}{
		{"ready", "ready", func(s *Snapshot) {}},
		{"draft", "draft", func(s *Snapshot) { s.PR.IsDraft = ptr(true) }},
		{"closed", "not_open", func(s *Snapshot) { s.PR.State = "CLOSED" }},
		{"conflict", "not_mergeable", func(s *Snapshot) { s.PR.Mergeable = "CONFLICTING" }},
		{"unknown merge", "not_mergeable", func(s *Snapshot) { s.PR.Mergeable = "UNKNOWN" }},
		{"blocked", "merge_policy_not_clean", func(s *Snapshot) { s.PR.MergeStateStatus = "BLOCKED" }},
		{"queue", "queue_required", func(s *Snapshot) {
			s.PR.MergeStateStatus = "BLOCKED"
			s.Rules = append(s.Rules, Rule{Type: "merge_queue"})
		}},
		{"changes aggregate", "review_not_approved", func(s *Snapshot) { s.PR.ReviewDecision = ptr("CHANGES_REQUESTED") }},
		{"unknown review decision", "review_not_approved", func(s *Snapshot) { s.PR.ReviewDecision = ptr("FUTURE") }},
		{"unresolved outdated thread", "unresolved_review", func(s *Snapshot) { s.PR.ReviewThreads = conn(Thread{"thread1", ptr(false)}) }},
		{"resolved thread", "ready", func(s *Snapshot) { s.PR.ReviewThreads = conn(Thread{"thread1", ptr(true)}) }},
		{"review pagination", "incomplete_reviews", func(s *Snapshot) { s.PR.Reviews.PageInfo.HasNextPage = ptr(true) }},
		{"thread pagination", "incomplete_reviews", func(s *Snapshot) { s.PR.ReviewThreads.TotalCount = ptr(101) }},
		{"missing reviews", "incomplete_reviews", func(s *Snapshot) { s.PR.Reviews.TotalCount = nil }},
		{"general comment", "unsupported_general_comments", func(s *Snapshot) { s.PR.Comments.TotalCount = ptr(1) }},
		{"commented review", "unsupported_commented_review", func(s *Snapshot) { s.PR.Reviews.Nodes[0].State = "COMMENTED" }},
		{"requested review", "changes_requested", func(s *Snapshot) { s.PR.Reviews.Nodes[0].State = "CHANGES_REQUESTED" }},
		{"pending review", "unknown_review", func(s *Snapshot) { s.PR.Reviews.Nodes[0].State = "PENDING" }},
		{"future review", "unknown_review", func(s *Snapshot) { s.PR.Reviews.Nodes[0].State = "FUTURE" }},
		{"stale review", "stale_review", func(s *Snapshot) { s.PR.Reviews.Nodes[0].Commit.OID = base }},
		{"base ref moved", "stale_base_ref", func(s *Snapshot) { s.PR.BaseRef.Target.OID = merge }},
		{"stale synthetic", "stale_synthetic_merge", func(s *Snapshot) { s.PR.PotentialMergeCommit.Parents.Nodes[0].OID = head }},
		{"missing parents", "stale_synthetic_merge", func(s *Snapshot) { s.PR.PotentialMergeCommit.Parents.TotalCount = nil }},
		{"empty head checks", "missing_checks", func(s *Snapshot) { s.HeadChecks = Checks{ptr(0), []Check{}} }},
		{"empty merge checks", "missing_checks", func(s *Snapshot) { s.MergeChecks = Checks{ptr(0), []Check{}} }},
		{"missing checks", "incomplete_checks", func(s *Snapshot) { s.MergeChecks.TotalCount = nil }},
		{"checks pagination", "incomplete_checks", func(s *Snapshot) { s.MergeChecks.TotalCount = ptr(101) }},
		{"stale check SHA", "unknown_check_identity", func(s *Snapshot) { s.MergeChecks.Checks[0].HeadSHA = head }},
		{"head only Actions", "unsupported_head_only_checks", func(s *Snapshot) { s.MergeChecks.Checks[0].Name = "other" }},
		{"duplicate attempts", "ambiguous_check_attempts", func(s *Snapshot) {
			c := s.MergeChecks.Checks[0]
			c.ID = 3
			s.MergeChecks.Checks = append(s.MergeChecks.Checks, c)
			s.MergeChecks.TotalCount = ptr(2)
		}},
		{"app mismatch", "unsupported_head_only_checks", func(s *Snapshot) { s.MergeChecks.Checks[0].App.ID = 11 }},
		{"missing app", "unknown_check_identity", func(s *Snapshot) { s.MergeChecks.Checks[0].App = nil }},
		{"legacy status", "unsupported_commit_statuses", func(s *Snapshot) { s.HeadStatuses = Statuses{ptr(1), []json.RawMessage{json.RawMessage(`{}`)}} }},
		{"missing statuses", "incomplete_statuses", func(s *Snapshot) { s.MergeStatuses.TotalCount = nil }},
		{"missing policy", "missing_required_check_policy", func(s *Snapshot) { s.Rules = []Rule{} }},
		{"required missing", "missing_required_check", func(s *Snapshot) {
			s.Rules[0].Parameters = json.RawMessage(`{"required_status_checks":[{"context":"missing","integration_id":10}]}`)
		}},
		{"required app unknown", "unsupported_required_check_identity", func(s *Snapshot) {
			s.Rules[0].Parameters = json.RawMessage(`{"required_status_checks":[{"context":"test"}]}`)
		}},
		{"unknown rules", "unsupported_branch_rule", func(s *Snapshot) { s.Rules = append(s.Rules, Rule{Type: "future"}) }},
	}
	for _, state := range []string{"skipped", "neutral", "cancelled", "failure", "timed_out", "stale", "action_required", "future", ""} {
		state := state
		cases = append(cases, struct {
			name, reason string
			edit         func(*Snapshot)
		}{"conclusion " + state, "check_not_successful", func(s *Snapshot) { s.MergeChecks.Checks[0].Conclusion = state }})
	}
	for _, status := range []string{"queued", "in_progress", "pending", "waiting", "unknown"} {
		status := status
		cases = append(cases, struct {
			name, reason string
			edit         func(*Snapshot)
		}{"status " + status, "check_not_successful", func(s *Snapshot) { s.MergeChecks.Checks[0].Status = status }})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := fixture()
			c.edit(&s)
			r := evaluate(s)
			if r.Reason != c.reason || r.Ready != (c.reason == "ready") {
				t.Fatalf("got %+v want %s", r, c.reason)
			}
		})
	}
}
func encoded(t *testing.T, s Snapshot) [][]byte {
	t.Helper()
	objs := []any{map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": s.PR}}}, s.HeadChecks, s.HeadStatuses, s.MergeChecks, s.MergeStatuses, s.Rules}
	out := [][]byte{}
	for _, o := range objs {
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}
func TestBoundedReadsAndEvidenceMovement(t *testing.T) {
	for _, change := range []string{"none", "head", "base", "retarget", "review", "attempt", "policy", "raw attempt revision", "pagination", "unknown", "oversized", "failure", "graphql error"} {
		t.Run(change, func(t *testing.T) {
			a := fixture()
			b := fixture()
			want := "evidence_changed"
			switch change {
			case "none":
				want = "ready"
			case "head":
				b.PR.HeadRefOid = merge
			case "base":
				b.PR.BaseRefOid = merge
			case "retarget":
				b.PR.BaseRefName = "other"
			case "review":
				b.PR.Reviews.Nodes[0].ID = "review2"
			case "attempt":
				b.MergeChecks.Checks[0].ID = 3
			case "policy":
				b.Rules = append(b.Rules, Rule{Type: "merge_queue"})
			case "pagination":
				b.PR.ReviewThreads.PageInfo.HasNextPage = ptr(true)
			}
			calls := append(encoded(t, a), encoded(t, b)...)
			switch change {
			case "unknown":
				calls[0] = []byte(`null`)
				want = "unknown_evidence"
			case "oversized":
				calls[0] = []byte(strings.Repeat(" ", MaxBytes+1))
				want = "unknown_evidence"
			case "failure":
				want = "unknown_evidence"
			case "graphql error":
				calls[0] = []byte(`{"errors":[{}],"data":{}}`)
				want = "unknown_evidence"
			case "raw attempt revision":
				calls[9] = []byte(strings.Replace(string(calls[9]), "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", 1))
			}
			n := 0
			q := func(ctx context.Context, args ...string) ([]byte, error) {
				if n >= 12 {
					t.Fatal("unbounded calls")
				}
				if args[0] != "api" || strings.Contains(strings.Join(args, " "), "mutation") {
					t.Fatal(args)
				}
				if change == "failure" {
					return nil, errors.New("private output")
				}
				v := calls[n]
				n++
				return v, nil
			}
			r := Inspect(context.Background(), q, "example-org/example-app", 42)
			if r.Reason != want {
				t.Fatalf("%+v want %s", r, want)
			}
			if change == "none" && n != 12 {
				t.Fatalf("calls=%d", n)
			}
		})
	}
}

func TestLatestReviewAndAppIdentity(t *testing.T) {
	s := fixture()
	s.PR.Reviews.Nodes[0].State = "CHANGES_REQUESTED"
	newer := s.PR.Reviews.Nodes[0]
	newer.ID = "review2"
	newer.State = "APPROVED"
	newer.SubmittedAt = "2026-01-02T00:00:00Z"
	s.PR.Reviews = conn(s.PR.Reviews.Nodes[0], newer)
	if r := evaluate(s); !r.Ready {
		t.Fatal(r)
	}
	// Same name from another app is a distinct observed obligation.
	for _, c := range []*Checks{&s.HeadChecks, &s.MergeChecks} {
		v := c.Checks[0]
		v.ID += 10
		v.App = &struct {
			ID int64 `json:"id"`
		}{11}
		c.Checks = append(c.Checks, v)
		c.TotalCount = ptr(2)
	}
	if r := evaluate(s); !r.Ready {
		t.Fatal(r)
	}
	s.MergeChecks.Checks[1].Conclusion = "skipped"
	if r := evaluate(s); r.Ready || r.Reason != "check_not_successful" {
		t.Fatal(r)
	}
	s = fixture()
	s.PR.Reviews = conn(s.PR.Reviews.Nodes[0], s.PR.Reviews.Nodes[0])
	if r := evaluate(s); r.Ready {
		t.Fatal(r)
	}
}
func TestReadBoundsAndNullInventory(t *testing.T) {
	for _, field := range []string{"checks", "statuses", "reviews", "rules"} {
		t.Run(field, func(t *testing.T) {
			s := fixture()
			switch field {
			case "checks":
				s.MergeChecks = Checks{ptr(0), nil}
			case "statuses":
				s.MergeStatuses = Statuses{ptr(0), nil}
			case "reviews":
				s.PR.Reviews = Connection[Review]{TotalCount: ptr(0), PageInfo: &Page{ptr(false)}}
			case "rules":
				s.Rules = make([]Rule, 100)
			}
			calls := encoded(t, s)
			n := 0
			r := Inspect(context.Background(), func(ctx context.Context, args ...string) ([]byte, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("no deadline")
				}
				v := calls[n]
				n++
				return v, nil
			}, "example-org/example-app", 42)
			if r.Ready {
				t.Fatal(r)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Inspect(ctx, func(context.Context, ...string) ([]byte, error) { return []byte(`{}`), nil }, "example-org/example-app", 42)
	if r.Ready || r.Reason != "unknown_evidence" {
		t.Fatal(r)
	}
	for _, repo := range []string{"https://github.com/example/app", "-evil/app", "example/..", "example/app?x=y", "example/app/extra"} {
		r := Inspect(context.Background(), func(context.Context, ...string) ([]byte, error) { t.Fatal("invalid repo executed"); return nil, nil }, repo, 42)
		if r.Reason != "invalid_arguments" {
			t.Fatal(r)
		}
	}
}
func TestExecOutputBound(t *testing.T) {
	var b boundedBuffer
	if n, err := b.Write([]byte(strings.Repeat("x", MaxBytes))); err != nil || n != MaxBytes {
		t.Fatal(n, err)
	}
	if _, err := b.Write([]byte("x")); err == nil || !b.exceeded || b.buffer.Len() != MaxBytes {
		t.Fatal("overflow not refused")
	}
}

func TestLegacyProtectionAndReviewPolicy(t *testing.T) {
	s := fixture()
	s.Rules = []Rule{}
	if err := json.Unmarshal([]byte(`{"requiresStatusChecks":true,"requiredStatusChecks":[{"context":"test","app":{"databaseId":10}}]}`), &s.PR.BaseRef.BranchProtectionRule); err != nil {
		t.Fatal(err)
	}
	if r := evaluate(s); !r.Ready {
		t.Fatal(r)
	}
	s.PR.ReviewDecision = nil
	if r := evaluate(s); r.Reason != "unknown_review_policy" {
		t.Fatal(r)
	}
	s = fixture()
	s.Rules = append(s.Rules, Rule{Type: "pull_request", Parameters: json.RawMessage(`{"required_approving_review_count":1}`)})
	s.PR.ReviewDecision = nil
	if r := evaluate(s); r.Reason != "unknown_review_policy" {
		t.Fatal(r)
	}
	s.Rules[1].Parameters = json.RawMessage(`{}`)
	if r := evaluate(s); r.Reason != "unknown_review_policy" {
		t.Fatal(r)
	}
}

func TestLegacyCheckPolicyEnforcementThroughInspect(t *testing.T) {
	for _, tc := range []struct {
		name, policy, reason string
		activeRules          bool
		calls                int
	}{
		{"disabled retained contexts alone", `{"requiresStatusChecks":false,"requiredStatusChecks":[{"context":"test","app":{"databaseId":10}}]}`, "missing_required_check_policy", false, 6},
		{"enabled legacy policy", `{"requiresStatusChecks":true,"requiredStatusChecks":[{"context":"test","app":{"databaseId":10}}]}`, "ready", false, 12},
		{"active rules independently apply", `{"requiresStatusChecks":false,"requiredStatusChecks":[{"context":"obsolete","app":{"databaseId":11}}]}`, "ready", true, 12},
		{"disabled empty policy", `{"requiresStatusChecks":false,"requiredStatusChecks":[]}`, "missing_required_check_policy", false, 6},
		{"enabled empty policy unknown", `{"requiresStatusChecks":true,"requiredStatusChecks":[]}`, "unknown_check_policy", true, 6},
		{"missing enforcement unknown", `{"requiredStatusChecks":[{"context":"test","app":{"databaseId":10}}]}`, "unknown_check_policy", true, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			if !tc.activeRules {
				s.Rules = []Rule{}
			}
			if err := json.Unmarshal([]byte(tc.policy), &s.PR.BaseRef.BranchProtectionRule); err != nil {
				t.Fatal(err)
			}
			reads := encoded(t, s)
			n := 0
			r := Inspect(context.Background(), func(context.Context, ...string) ([]byte, error) {
				if n >= 12 {
					t.Fatal("read bound exceeded")
				}
				b := reads[n%len(reads)]
				n++
				return b, nil
			}, "example-org/example-app", 42)
			if r.Reason != tc.reason || r.Ready != (tc.reason == "ready") || n != tc.calls {
				t.Fatalf("got %+v calls=%d; want reason=%s calls=%d", r, n, tc.reason, tc.calls)
			}
		})
	}
}
