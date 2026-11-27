// Package prready reads a bounded GitHub snapshot. It never changes a PR or
// grants permission to merge; unsupported evidence is not readiness.
package prready

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxBytes = 256 << 10
const Timeout = 30 * time.Second

type Query func(context.Context, ...string) ([]byte, error)
type Report struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
	Head   string `json:"head,omitempty"`
	Base   string `json:"base,omitempty"`
	Merge  string `json:"merge,omitempty"`
}
type Page struct{ HasNextPage *bool }
type Connection[T any] struct {
	TotalCount *int
	PageInfo   *Page
	Nodes      []T
}

func (c Connection[T]) complete() bool {
	return c.Nodes != nil && c.TotalCount != nil && c.PageInfo != nil && c.PageInfo.HasNextPage != nil && !*c.PageInfo.HasNextPage && *c.TotalCount == len(c.Nodes) && len(c.Nodes) <= 100
}

type OID struct{ OID string }
type Review struct {
	ID, State, SubmittedAt, UpdatedAt string
	Author                            *struct{ Login string }
	Commit                            *OID
}
type Thread struct {
	ID         string
	IsResolved *bool
}
type PR struct {
	ID, State, HeadRefOid, BaseRefOid, BaseRefName, UpdatedAt, Mergeable, MergeStateStatus string
	IsDraft                                                                                *bool
	ReviewDecision                                                                         *string
	PotentialMergeCommit                                                                   *struct {
		OID     string
		Parents Connection[OID]
	}
	ReviewThreads Connection[Thread]
	Reviews       Connection[Review]
	Comments      *struct{ TotalCount *int }
	BaseRef       *struct {
		Target               *OID
		BranchProtectionRule *struct {
			RequiresStatusChecks *bool
			RequiredStatusChecks []struct {
				Context string
				App     *struct{ DatabaseID int64 }
			}
		}
	}
}
type Check struct {
	ID                       int64 `json:"id"`
	Name, Status, Conclusion string
	StartedAt                string `json:"started_at"`
	CompletedAt              string `json:"completed_at"`
	ExternalID               string `json:"external_id"`
	DetailsURL               string `json:"details_url"`
	CheckSuite               *struct {
		ID int64 `json:"id"`
	} `json:"check_suite"`
	HeadSHA string `json:"head_sha"`
	App     *struct {
		ID int64 `json:"id"`
	}
}
type Checks struct {
	TotalCount *int    `json:"total_count"`
	Checks     []Check `json:"check_runs"`
}
type Statuses struct {
	TotalCount *int              `json:"total_count"`
	Statuses   []json.RawMessage `json:"statuses"`
}
type Rule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}
type Snapshot struct {
	PR                          PR
	HeadChecks, MergeChecks     Checks
	HeadStatuses, MergeStatuses Statuses
	Rules                       []Rule
}

var repoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func Valid(repo string, number int) bool {
	return repoRE.MatchString(repo) && number > 0 && !strings.HasSuffix(repo, "/.") && !strings.HasSuffix(repo, "/..")
}

const query = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){id state isDraft headRefOid baseRefOid baseRefName updatedAt mergeable mergeStateStatus reviewDecision baseRef{target{oid} branchProtectionRule{requiresStatusChecks requiredStatusChecks{context app{databaseId}}}} potentialMergeCommit{oid parents(first:3){totalCount pageInfo{hasNextPage} nodes{oid}}} reviewThreads(first:100){totalCount pageInfo{hasNextPage} nodes{id isResolved}} reviews(first:100){totalCount pageInfo{hasNextPage} nodes{id state submittedAt updatedAt author{login} commit{oid}}} comments{totalCount}}}}`

// Inspect makes two full reads, at most twelve CLI calls, without retries or
// polling. Any difference (including a new review/check attempt) refuses success.
func Inspect(ctx context.Context, gh Query, repo string, number int) Report {
	if !Valid(repo, number) {
		return Report{Reason: "invalid_arguments"}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, err := read(ctx, gh, repo, number)
	if err != nil {
		return Report{Reason: "unknown_evidence"}
	}
	r := evaluate(a)
	// Even nonready snapshots need no second read: they cannot grant readiness.
	if !r.Ready {
		return r
	}
	b, err := read(ctx, gh, repo, number)
	if err != nil {
		return Report{Reason: "unknown_evidence"}
	}
	if !reflect.DeepEqual(a, b) {
		r.Ready = false
		r.Reason = "evidence_changed"
		return r
	}
	return r
}
func read(ctx context.Context, gh Query, repo string, number int) (Snapshot, error) {
	var s Snapshot
	call := func(dst any, args ...string) error {
		b, err := gh(ctx, args...)
		if err != nil || ctx.Err() != nil || len(b) > MaxBytes {
			return errors.New("read failed")
		}
		if err = json.Unmarshal(b, dst); err != nil {
			return errors.New("invalid JSON")
		}
		return nil
	}
	parts := strings.Split(repo, "/")
	var envelope struct {
		Errors []json.RawMessage
		Data   *struct{ Repository *struct{ PullRequest *PR } }
	}
	err := call(&envelope, "api", "--hostname", "github.com", "graphql", "-f", "query="+query, "-f", "owner="+parts[0], "-f", "name="+parts[1], "-F", "number="+strconv.Itoa(number))
	if err != nil || len(envelope.Errors) > 0 || envelope.Data == nil || envelope.Data.Repository == nil || envelope.Data.Repository.PullRequest == nil {
		return s, errors.New("PR unavailable")
	}
	s.PR = *envelope.Data.Repository.PullRequest
	if !shaRE.MatchString(s.PR.HeadRefOid) || !shaRE.MatchString(s.PR.BaseRefOid) || s.PR.PotentialMergeCommit == nil || !shaRE.MatchString(s.PR.PotentialMergeCommit.OID) {
		return s, errors.New("identity unavailable")
	}
	api := func(dst any, path string) error {
		return call(dst, "api", "--hostname", "github.com", "--method", "GET", "repos/"+repo+"/"+path)
	}
	for _, item := range []struct {
		sha      string
		checks   *Checks
		statuses *Statuses
	}{{s.PR.HeadRefOid, &s.HeadChecks, &s.HeadStatuses}, {s.PR.PotentialMergeCommit.OID, &s.MergeChecks, &s.MergeStatuses}} {
		if err = api(item.checks, "commits/"+item.sha+"/check-runs?filter=all&per_page=100"); err != nil {
			return s, err
		}
		if err = api(item.statuses, "commits/"+item.sha+"/status?per_page=100"); err != nil {
			return s, err
		}
	}
	// A full page cannot prove the effective rules inventory is complete.
	if err = api(&s.Rules, "rules/branches/"+url.PathEscape(s.PR.BaseRefName)+"?per_page=100"); err != nil || s.Rules == nil || len(s.Rules) >= 100 {
		return s, errors.New("rules unavailable")
	}
	return s, nil
}

func evaluate(s Snapshot) Report {
	p := s.PR
	r := Report{Head: p.HeadRefOid, Base: p.BaseRefOid}
	if p.PotentialMergeCommit != nil {
		r.Merge = p.PotentialMergeCommit.OID
	}
	fail := func(reason string) Report { r.Reason = reason; return r }
	if p.ID == "" || p.IsDraft == nil || p.UpdatedAt == "" || p.BaseRefName == "" || p.BaseRef == nil {
		return fail("unknown_pr")
	}
	if p.BaseRef.Target == nil || p.BaseRef.Target.OID != p.BaseRefOid {
		return fail("stale_base_ref")
	}
	if p.State != "OPEN" {
		return fail("not_open")
	}
	if *p.IsDraft {
		return fail("draft")
	}
	for _, rule := range s.Rules {
		if rule.Type == "merge_queue" {
			return fail("queue_required")
		}
	}
	if p.Mergeable != "MERGEABLE" {
		return fail("not_mergeable")
	}
	if p.MergeStateStatus != "CLEAN" {
		return fail("merge_policy_not_clean")
	}
	if p.ReviewDecision != nil && *p.ReviewDecision != "APPROVED" {
		return fail("review_not_approved")
	}
	if !p.ReviewThreads.complete() || !p.Reviews.complete() || p.Comments == nil || p.Comments.TotalCount == nil {
		return fail("incomplete_reviews")
	}
	// General comments have no structured resolved/finding state. Do not read or
	// classify their prose, even when a comment might be an automated all-clear.
	if *p.Comments.TotalCount != 0 {
		return fail("unsupported_general_comments")
	}
	seenThreads := map[string]bool{}
	for _, t := range p.ReviewThreads.Nodes {
		if t.ID == "" || seenThreads[t.ID] || t.IsResolved == nil {
			return fail("unknown_review_thread")
		}
		seenThreads[t.ID] = true
		if !*t.IsResolved {
			return fail("unresolved_review")
		}
	}
	latest := map[string]Review{}
	seenReviews := map[string]bool{}
	for _, v := range p.Reviews.Nodes {
		if v.ID == "" || seenReviews[v.ID] || v.Author == nil || v.Author.Login == "" {
			return fail("unknown_review")
		}
		seenReviews[v.ID] = true
		switch v.State {
		case "APPROVED", "DISMISSED", "CHANGES_REQUESTED", "COMMENTED":
		default:
			return fail("unknown_review")
		}
		at, err := time.Parse(time.RFC3339, v.SubmittedAt)
		if err != nil {
			return fail("unknown_review")
		}
		prev, ok := latest[v.Author.Login]
		if ok {
			before, _ := time.Parse(time.RFC3339, prev.SubmittedAt)
			if at.Equal(before) {
				return fail("ambiguous_reviews")
			}
			if at.Before(before) {
				continue
			}
		}
		latest[v.Author.Login] = v
	}
	for _, v := range latest {
		switch v.State {
		case "CHANGES_REQUESTED":
			return fail("changes_requested")
		case "COMMENTED":
			return fail("unsupported_commented_review")
		case "APPROVED":
			if v.Commit == nil || v.Commit.OID != p.HeadRefOid {
				return fail("stale_review")
			}
		}
	}
	m := p.PotentialMergeCommit
	if m == nil || !m.Parents.complete() || len(m.Parents.Nodes) != 2 || m.Parents.Nodes[0].OID != p.BaseRefOid || m.Parents.Nodes[1].OID != p.HeadRefOid {
		return fail("stale_synthetic_merge")
	}
	for _, st := range []Statuses{s.HeadStatuses, s.MergeStatuses} {
		if st.Statuses == nil || st.TotalCount == nil || *st.TotalCount != len(st.Statuses) {
			return fail("incomplete_statuses")
		}
		if *st.TotalCount != 0 {
			return fail("unsupported_commit_statuses")
		}
	}
	head, reason := inventory(s.HeadChecks, p.HeadRefOid)
	if reason != "" {
		return fail(reason)
	}
	merge, reason := inventory(s.MergeChecks, m.OID)
	if reason != "" {
		return fail(reason)
	}
	if len(head) == 0 || len(merge) == 0 {
		return fail("missing_checks")
	}
	for key := range head {
		if !merge[key] {
			return fail("unsupported_head_only_checks")
		}
	}
	required := map[string]bool{}
	protection := p.BaseRef.BranchProtectionRule
	if protection != nil {
		if p.ReviewDecision == nil {
			return fail("unknown_review_policy")
		}
		if protection.RequiresStatusChecks == nil {
			return fail("unknown_check_policy")
		}
		if *protection.RequiresStatusChecks && len(protection.RequiredStatusChecks) == 0 {
			return fail("unknown_check_policy")
		}
		for _, c := range protection.RequiredStatusChecks {
			if c.Context == "" || c.App == nil || c.App.DatabaseID <= 0 {
				return fail("unsupported_required_check_identity")
			}
			required[checkKey(c.Context, c.App.DatabaseID)] = true
		}
	}
	for _, rule := range s.Rules {
		switch rule.Type {
		case "required_status_checks":
			var params struct {
				Checks *[]struct {
					Context       string `json:"context"`
					IntegrationID *int64 `json:"integration_id"`
				} `json:"required_status_checks"`
			}
			if json.Unmarshal(rule.Parameters, &params) != nil || params.Checks == nil || len(*params.Checks) == 0 {
				return fail("unknown_check_policy")
			}
			for _, c := range *params.Checks {
				if c.Context == "" || c.IntegrationID == nil || *c.IntegrationID <= 0 {
					return fail("unsupported_required_check_identity")
				}
				required[checkKey(c.Context, *c.IntegrationID)] = true
			}
		case "pull_request":
			var params struct {
				Count *int `json:"required_approving_review_count"`
			}
			if json.Unmarshal(rule.Parameters, &params) != nil || params.Count == nil || *params.Count < 0 {
				return fail("unknown_review_policy")
			}
			if *params.Count > 0 && p.ReviewDecision == nil {
				return fail("unknown_review_policy")
			}
		default:
			return fail("unsupported_branch_rule")
		}
	}
	// A nonempty observed inventory cannot establish an undeclared future workflow.
	// Require an explicit required-check policy as well as all observed checks.
	if len(required) == 0 {
		return fail("missing_required_check_policy")
	}
	for key := range required {
		if !merge[key] {
			return fail("missing_required_check")
		}
	}
	r.Ready = true
	r.Reason = "ready"
	return r
}
func checkKey(name string, app int64) string { return fmt.Sprintf("%d:%s", app, name) }
func inventory(c Checks, sha string) (map[string]bool, string) {
	if c.Checks == nil || c.TotalCount == nil || *c.TotalCount != len(c.Checks) || len(c.Checks) > 100 {
		return nil, "incomplete_checks"
	}
	keys := map[string]bool{}
	ids := map[int64]bool{}
	for _, v := range c.Checks {
		if v.ID <= 0 || ids[v.ID] || v.Name == "" || v.HeadSHA != sha || v.App == nil || v.App.ID <= 0 {
			return nil, "unknown_check_identity"
		}
		ids[v.ID] = true

		key := checkKey(v.Name, v.App.ID)
		if keys[key] {
			return nil, "ambiguous_check_attempts"
		}
		keys[key] = true
		// ci-wait permits skipped/neutral as wait completion. Readiness is stronger:
		// no meaning for such a skip is established here, even for optional jobs.
		if v.Status != "completed" || v.Conclusion != "success" {
			return nil, "check_not_successful"
		}
		if _, err := time.Parse(time.RFC3339, v.CompletedAt); err != nil {
			return nil, "unknown_check_revision"
		}
		if v.CheckSuite == nil || v.CheckSuite.ID <= 0 {
			return nil, "unknown_check_revision"
		}
	}
	return keys, ""
}
