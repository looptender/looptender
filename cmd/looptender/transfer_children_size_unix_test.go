//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/testexec"
)

// Exercise actual command dispatch and subprocess output limits, rather than
// an injected runner that bypasses the byte limit. All children are fictional.
type largeChildFixture struct {
	path, dir, listing, calls string
	payload                   []byte
}

func newLargeChildFixture(t *testing.T) largeChildFixture {
	t.Helper()
	path, dir := transferApplyFixture(t)
	root := filepath.Dir(path)
	listing, calls := filepath.Join(root, "listing.json"), filepath.Join(root, "calls")
	children := []map[string]any{}
	for i := 0; i < 46; i++ {
		children = append(children, map[string]any{"id": fmt.Sprintf("thr_example_lane_%02d", i), "title": "PRIVATE-" + strings.Repeat("x", 1500)})
	}
	payload, err := json.Marshal(children)
	if err != nil || len(payload) <= 64*1024 || len(payload) > inventoryListLimit {
		t.Fatal("bad fixture size", len(payload), err)
	}
	if err := os.WriteFile(listing, payload, 0600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "bb")
	// The fake accepts only the transfer's existing show/list/parent-update
	// commands. Confirmation exposes all children under the destination only
	// after all 46 parent updates were actually recorded.
	testexec.Script(t, cli, `#!/bin/sh
printf '%s\n' "$*" >> '`+calls+`'
case "$*" in
 'thread show thr_source --json'|'thread show thr_destination --json')
 printf '{"thread":{"id":"%s","projectId":"proj_example","environmentId":"env_example","providerId":"codex","status":"idle","archivedAt":null,"deletedAt":null},"environment":{"id":"env_example","projectId":"proj_example","hostId":"host_example","path":"/private/example-workspace"}}' "$3" ;;
 'thread list --parent-thread thr_source --include-hidden --json')
 if [ -f '`+root+`/moved' ] && [ "$(wc -l < '`+root+`/moved')" -eq 46 ]; then printf '[]'; else cat '`+listing+`'; fi ;;
 'thread list --parent-thread thr_destination --include-hidden --json')
 if [ -f '`+root+`/destination-listing' ]; then cat '`+root+`/destination-listing'; exit 0; fi
 if [ -f '`+root+`/moved' ] && [ "$(wc -l < '`+root+`/moved')" -eq 46 ]; then cat '`+listing+`'; else printf '[]'; fi ;;
 thread\ update\ thr_example_lane_[0-9][0-9]\ --parent-thread\ thr_destination\ --json)
 printf '%s\n' "$3" >> '`+root+`/moved'
 printf '{"ok":true}' ;;
 *) exit 2 ;;
esac
`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("  bb:\n"), []byte("  bb:\n    cli: "+cli+"\n"), 1)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return largeChildFixture{path, dir, listing, calls, payload}
}

func (f largeChildFixture) command(t *testing.T, args ...string) (int, transferChildResult) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run(context.Background(), append(append([]string{"transfer"}, args...), "-c", f.path, "--json"), &out, &errs)
	var r struct {
		Children transferChildResult `json:"children"`
	}
	if json.Unmarshal(out.Bytes(), &r) != nil {
		t.Fatalf("no JSON result: %d %s %s", code, out.String(), errs.String())
	}
	if strings.Contains(out.String()+errs.String(), "PRIVATE-") || strings.Contains(out.String()+errs.String(), "thr_example_lane_") {
		t.Fatal("child identity/title exposed")
	}
	return code, r.Children
}

func (f largeChildFixture) updateCount(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(f.calls)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "thread update thr_example_lane_") && strings.HasSuffix(line, " --parent-thread thr_destination --json"):
			count++
		case line == "thread show thr_source --json", line == "thread show thr_destination --json", line == "thread list --parent-thread thr_source --include-hidden --json", line == "thread list --parent-thread thr_destination --include-hidden --json":
		default:
			t.Fatalf("unexpected command: %s", line)
		}
	}
	return count
}

func TestTransferApplyChildListingLargerThanDoctorLimit(t *testing.T) {
	f := newLargeChildFixture(t)
	code, r := f.command(t, "apply", "--from", "thr_source", "--thread", "thr_destination")
	if code != exitOK || !r.Listed || !r.Done || r.Moved != 46 || f.updateCount(t) != 46 {
		t.Fatalf("apply: %d %+v", code, r)
	}
	ctl, err := control.Load(f.dir)
	if err != nil || !ctl.AwaitingUser || !ctl.Paused || !ctl.Stop || !ctl.Completed {
		t.Fatal("holds changed", ctl, err)
	}
}

func TestTransferRecoverLargeChildrenAfterUnknownListing(t *testing.T) {
	for _, kind := range []string{"oversized", "malformed", "null"} {
		t.Run(kind, func(t *testing.T) {
			f := newLargeChildFixture(t)
			bad := []byte(`null`)
			if kind == "oversized" {
				bad = []byte(`[{"id":"thr_example_lane_00","title":"` + strings.Repeat("x", inventoryListLimit) + `"}]`)
			}
			if kind == "malformed" {
				bad = append(append([]byte{}, f.payload[:len(f.payload)-1]...), ',')
			}
			if err := os.WriteFile(f.listing, bad, 0600); err != nil {
				t.Fatal(err)
			}
			code, r := f.command(t, "apply", "--from", "thr_source", "--thread", "thr_destination")
			// Transfer itself finishes held; unknown child listing authorizes no update.
			if code != exitOK || r.Listed || r.Done || r.Moved != 0 || f.updateCount(t) != 0 {
				t.Fatalf("unknown listing: %d %+v", code, r)
			}
			st, err := state.Load(f.dir)
			if err != nil || st.ChildMove == nil || st.ChildMove.Listed || st.ChildMove.Done {
				t.Fatal(st.ChildMove, err)
			}
			ctl, err := control.Load(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.listing, f.payload, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BB_THREAD_ID", "")
			code, r = f.command(t, "recover", "--id", st.ChildMove.Transfer)
			if code != exitOK || !r.Listed || !r.Done || r.Moved != 46 || f.updateCount(t) != 46 {
				t.Fatalf("recover: %d %+v", code, r)
			}
			after, err := control.Load(f.dir)
			if err != nil || !after.UpdatedAt.Equal(ctl.UpdatedAt) || after.Gate() != ctl.Gate() {
				t.Fatal("child recovery changed controls", err)
			}
			// A completed recovery cannot replay the parent updates.
			code, r = f.command(t, "recover", "--id", st.ChildMove.Transfer)
			if code != exitOK || !r.Done || f.updateCount(t) != 46 {
				t.Fatalf("replayed recovery: %d %+v", code, r)
			}
		})
	}
}

// Larger reads must not silently widen show/update or unrelated listing
// response limits. Above the new cap, even the exact child read is refused.
func TestTransferExecBoundsOnlyDirectChildListings(t *testing.T) {
	root := t.TempDir()
	payload := filepath.Join(root, "output")
	cli := filepath.Join(root, "bb")
	testexec.Script(t, cli, "#!/bin/sh\ncat '"+payload+"'\n")
	for _, tc := range []struct {
		name string
		args []string
		size int
		want bool
	}{
		{"children", []string{"thread", "list", "--parent-thread", "thr_example_source", "--include-hidden", "--json"}, 70 * 1024, true},
		{"at-limit", []string{"thread", "list", "--parent-thread", "thr_example_source", "--include-hidden", "--json"}, inventoryListLimit, true},
		{"above-limit", []string{"thread", "list", "--parent-thread", "thr_example_source", "--include-hidden", "--json"}, inventoryListLimit + 1, false},
		{"show", []string{"thread", "show", "thr_example_source", "--json"}, 70 * 1024, false},
		{"update", []string{"thread", "update", "thr_example_child", "--parent-thread", "thr_example_destination", "--json"}, 70 * 1024, false},
		{"other-list", []string{"thread", "list", "--json"}, 70 * 1024, false},
		{"extra-flag", []string{"thread", "list", "--parent-thread", "thr_example_source", "--include-hidden", "--json", "--extra"}, 70 * 1024, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(payload, bytes.Repeat([]byte("x"), tc.size), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := transferExec(ctx, cli, tc.args, nil, "")
			if (err == nil) != tc.want {
				t.Fatalf("bytes=%d err=%v", len(out), err)
			}
			if tc.want && len(out) != tc.size {
				t.Fatal("truncated output", len(out))
			}
			if !tc.want && out != nil {
				t.Fatal("partial output returned after cap refusal")
			}
		})
	}
}

func TestTransferChildConfirmationAboveLimitStaysPending(t *testing.T) {
	f := newLargeChildFixture(t)
	bad := filepath.Join(filepath.Dir(f.path), "destination-listing")
	if err := os.WriteFile(bad, bytes.Repeat([]byte("x"), inventoryListLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	code, r := f.command(t, "apply", "--from", "thr_source", "--thread", "thr_destination")
	// Discovery allowed the parent updates, but unavailable destination evidence
	// cannot establish success or spend retry attempts on invented results.
	if code != exitOK || !r.Listed || r.Done || r.Moved != 0 || r.Pending != 46 || f.updateCount(t) != 46 {
		t.Fatalf("confirmation: %d %+v", code, r)
	}
	st, err := state.Load(f.dir)
	if err != nil || st.ChildMove == nil || st.ChildMove.Done {
		t.Fatal(st.ChildMove, err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_THREAD_ID", "")
	code, r = f.command(t, "recover", "--id", st.ChildMove.Transfer)
	if code != exitOK || !r.Done || r.Moved != 46 || f.updateCount(t) != 46 {
		t.Fatalf("recovery replayed updates: %d %+v", code, r)
	}
}
