//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
)

// Exercise the CLI/BB boundary, not assignmentChildrenMatch directly. The second
// inventory is changed after the first scan admitted an independent-work wake.
// Equal counts and display names must not substitute for exact lane identity.
func TestAssignmentInventoryIdentityAcrossRevalidation(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		wake           bool
	}{
		{"reordered", `echo '[{"id":"thr_example_preview"},{"id":"thr_example_docs"},{"id":"thr_example_ci"}]' > "$d/thr_primary.children"`, true},
		{"renamed", `echo '{"thread":{"id":"thr_example_docs","parentThreadId":"thr_primary","status":"idle","title":"Fictional renamed review"}}' > "$d/thr_example_docs.show"`, true},
		{"same-count-replacement", `echo '[{"id":"thr_example_ci"},{"id":"thr_example_replacement"},{"id":"thr_example_preview"}]' > "$d/thr_primary.children"; echo '{"thread":{"id":"thr_example_replacement","parentThreadId":"thr_primary","status":"idle","title":"docs"}}' > "$d/thr_example_replacement.show"`, false},
		{"reparented", `echo '{"thread":{"id":"thr_example_docs","parentThreadId":"thr_example_other","status":"idle"}}' > "$d/thr_example_docs.show"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, cfg, mutate := assignmentSchedulingFixture(t, true)
			mutate("ready", map[string]any{"independentlyActionable": true, "selections": []control.AssignmentWakeSelection{{Assignment: "docs", AssignmentRevision: 1, HandoffRevision: 1}}})
			beforeControl := mustAssignmentControl(t, cfg)
			path := filepath.Join(cfg.Loop.StateDir, control.AssignmentName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeCalls := len(f.read("calls"))
			inject := `"thread list") n=0; if [ -f "$d/identity-scans" ]; then n=$(cat "$d/identity-scans"); fi; n=$((n+1)); echo "$n" > "$d/identity-scans"; if [ "$n" = 2 ]; then ` + tc.mutation + `; fi; t="$4";`
			write(t, filepath.Join(f.dir, "bin", "bb"), strings.Replace(fakeBB, `"thread list") t="$4";`, inject, 1), 0755)
			got := f.tick()
			calls := f.read("calls")[beforeCalls:]
			if strings.Count(calls, "thread list") != 2 {
				t.Fatal("did not exercise both inventory reads", calls)
			}
			want := "none"
			sends := 0
			if tc.wake {
				want = "wake"
				sends = 1
			}
			if got["action"] != want || strings.Count(calls, "thread tell") != sends {
				t.Fatal("identity changed wake eligibility", got, calls)
			}
			for _, op := range []string{"thread spawn", "thread retry", "thread stop", "thread update"} {
				if strings.Contains(calls, op) {
					t.Fatal("inventory granted lane authority", calls)
				}
			}
			if sends == 1 && !strings.Contains(calls, "thread tell thr_primary") {
				t.Fatal("wake did not target coordinator", calls)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			r, err := control.LoadAssignments(cfg.Loop.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			grant := r.Revisions[len(r.Revisions)-1].Wake
			if (grant.AttemptedAt != nil) != tc.wake {
				t.Fatal("wrong grant consumption", grant)
			}
			if !tc.wake && string(before) != string(after) {
				t.Fatal("uncertain identity changed assignment history")
			}
			afterControl := mustAssignmentControl(t, cfg)
			if beforeControl.Enabled != afterControl.Enabled || beforeControl.Paused != afterControl.Paused || beforeControl.Stop != afterControl.Stop || beforeControl.AwaitingUser != afterControl.AwaitingUser || beforeControl.Completed != afterControl.Completed {
				t.Fatal("inventory changed controls", beforeControl, afterControl)
			}
			// Fresh command construction must not replay the grant. For refusals, repair
			// the inventory explicitly and show the unspent grant still works once.
			write(t, filepath.Join(f.dir, "bin", "bb"), fakeBB, 0755)
			write(t, filepath.Join(f.bbDir, "thr_primary.children"), `[{"id":"thr_example_ci"},{"id":"thr_example_docs"},{"id":"thr_example_preview"}]`, 0600)
			write(t, filepath.Join(f.bbDir, "thr_example_docs.show"), `{"thread":{"id":"thr_example_docs","parentThreadId":"thr_primary","status":"idle"}}`, 0600)
			got = f.tick()
			if tc.wake && got["action"] != "none" || !tc.wake && got["action"] != "wake" {
				t.Fatal("reload/repair mishandled grant", got)
			}
			if strings.Count(f.read("calls")[beforeCalls:], "thread tell") != 1 {
				t.Fatal("grant lost or replayed", f.read("calls")[beforeCalls:])
			}
		})
	}
}
