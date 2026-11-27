package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/prready"
)

func TestPRReadyCommand(t *testing.T) {
	for _, args := range [][]string{{}, {"--repo", "example-org/example-app"}, {"--repo", "../bad", "--pr", "7"}, {"--repo", "example-org/example-app", "--pr", "7", "extra"}} {
		var out, errout bytes.Buffer
		code := prReady(env{ctx: context.Background(), stdout: &out, stderr: &errout}, args, func(context.Context, ...string) ([]byte, error) {
			t.Fatal("invalid arguments called gh")
			return nil, nil
		})
		if code != exitUsage {
			t.Fatalf("%v: %d", args, code)
		}
	}
	for _, asJSON := range []bool{false, true} {
		var out, errout bytes.Buffer
		args := []string{"--repo", "example-org/example-app", "--pr", "7"}
		if asJSON {
			args = append(args, "--json")
		}
		code := prReady(env{ctx: context.Background(), stdout: &out, stderr: &errout}, args, func(context.Context, ...string) ([]byte, error) { return nil, errors.New("private stderr") })
		if code != exitFailed || strings.Contains(out.String()+errout.String(), "private stderr") {
			t.Fatalf("code=%d output=%s %s", code, &out, &errout)
		}
	}
	// End-to-end fictional supported evidence; stdout is exactly the commit to pin.
	head, base, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	page := `"totalCount":0,"pageInfo":{"hasNextPage":false},"nodes":[]`
	pr := fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"id":"PR_example","state":"OPEN","isDraft":false,"headRefOid":%q,"baseRefOid":%q,"baseRefName":"main","updatedAt":"2026-01-01T00:00:00Z","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":null,"baseRef":{"target":{"oid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},"potentialMergeCommit":{"oid":%q,"parents":{"totalCount":2,"pageInfo":{"hasNextPage":false},"nodes":[{"oid":%q},{"oid":%q}]}},"reviewThreads":{%s},"reviews":{%s},"comments":{"totalCount":0}}}}}`, head, base, merge, base, head, page, page)
	checks := func(sha string) []byte {
		return []byte(fmt.Sprintf(`{"total_count":1,"check_runs":[{"id":1,"name":"test","head_sha":%q,"status":"completed","conclusion":"success","completed_at":"2026-01-01T00:00:00Z","check_suite":{"id":1},"app":{"id":10}}]}`, sha))
	}
	calls := [][]byte{[]byte(pr), checks(head), []byte(`{"total_count":0,"statuses":[]}`), checks(merge), []byte(`{"total_count":0,"statuses":[]}`), []byte(`[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"test","integration_id":10}]}}]`)}
	for _, asJSON := range []bool{false, true} {
		var out, errout bytes.Buffer
		n := 0
		args := []string{"--repo", "example-org/example-app", "--pr", "7"}
		if asJSON {
			args = append(args, "--json")
		}
		code := prReady(env{ctx: context.Background(), stdout: &out, stderr: &errout}, args, func(context.Context, ...string) ([]byte, error) { b := calls[n%len(calls)]; n++; return b, nil })
		if code != exitOK || n != 12 {
			t.Fatalf("code=%d calls=%d err=%s", code, n, &errout)
		}
		if asJSON {
			var r prready.Report
			if json.Unmarshal(out.Bytes(), &r) != nil || !r.Ready || r.Head != head {
				t.Fatal(&out)
			}
		} else if out.String() != head+"\n" {
			t.Fatal(&out)
		}
	}
}
