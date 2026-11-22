package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// childBB is a fake bb for transfer apply with child threads: thread show
// for the two loop threads and each child, list by parent, and update.
type childBB struct {
	t        *testing.T
	parent   map[string]string // child -> parent
	archived map[string]bool
	provider map[string]string
	updates  []string
	// onUpdate runs before each update.
	onUpdate func(child string)
	// ignore makes bb answer success without moving the child.
	ignore map[string]bool
}

func newChildBB(t *testing.T, live, archived int) *childBB {
	b := &childBB{t: t, parent: map[string]string{}, archived: map[string]bool{}, provider: map[string]string{}}
	for i := 0; i < live+archived; i++ {
		id := fmt.Sprintf("thr_lane_%02d", i)
		b.parent[id], b.provider[id] = "thr_source", "codex"
		b.archived[id] = i >= live
	}
	return b
}

func (b *childBB) under(parent string) []string {
	ids := []string{}
	for id, p := range b.parent {
		if p == parent {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (b *childBB) run() doctorRun {
	base := transferRunner(b.t, nil)
	return func(ctx context.Context, cli string, args []string, env map[string]string, workdir string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			b.t.Fatal("unbounded query")
		}
		switch {
		case len(args) == 6 && args[1] == "list" && reflect.DeepEqual(args, []string{"thread", "list", "--parent-thread", args[3], "--include-hidden", "--json"}):
			out := []map[string]any{}
			for _, id := range b.under(args[3]) {
				c := map[string]any{"id": id, "title": "PRIVATE-LANE-TITLE"}
				if b.archived[id] {
					c["archivedAt"] = 1
				}
				out = append(out, c)
			}
			return json.Marshal(out)
		case len(args) == 6 && args[1] == "update" && reflect.DeepEqual(args, []string{"thread", "update", args[2], "--parent-thread", args[4], "--json"}):
			if b.onUpdate != nil {
				b.onUpdate(args[2])
			}
			b.updates = append(b.updates, args[2])
			if _, ok := b.parent[args[2]]; !ok {
				return []byte(`{"ok":false,"error":{"code":"thread_not_found"}}`), fmt.Errorf("exit 1")
			}
			if !b.ignore[args[2]] {
				b.parent[args[2]] = args[4]
			}
			return []byte(`{"ok":true}`), nil
		case len(args) == 4 && args[1] == "show" && strings.HasPrefix(args[2], "thr_lane_"):
			v := transferResponse(args[2])
			v["thread"].(map[string]any)["providerId"] = b.provider[args[2]]
			return json.Marshal(v)
		}
		return base(ctx, cli, args, env, workdir)
	}
}

func applyWithChildren(t *testing.T, path string, b *childBB, step func(string) error, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := transferApply(env{ctx: context.Background(), stdout: &out, stderr: &out}, append(args, "-c", path), b.run(), step)
	if strings.Contains(out.String(), "PRIVATE-") || strings.Contains(out.String(), "thr_lane_") {
		t.Fatalf("private data or a child ID exposed: %s", out.String())
	}
	return code, out.String()
}

// Apply moves the source's live children to the destination once the
// transfer has finished, says so in counts, and leaves the loop held.
func TestTransferApplyMovesChildThreads(t *testing.T) {
	path, dir := transferApplyFixture(t)
	// A loop not stopped or completed, so the moved note shows.
	control.Update(dir, func(c *control.File) { c.Stop, c.Completed, c.Paused, c.Note = false, false, false, "" })
	b := newChildBB(t, 3, 1)
	code, out := applyWithChildren(t, path, b, nil, "apply", "--from", "thr_source", "--thread", "thr_destination", "--json")
	if code != exitOK {
		t.Fatal(code, out)
	}
	var r struct {
		AwaitingUser bool                `json:"awaitingUser"`
		Children     transferChildResult `json:"children"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || !r.AwaitingUser || r.Children != (transferChildResult{Moved: 3, Skipped: 1, Listed: true, Done: true}) {
		t.Fatalf("%s", out)
	}
	if got := b.under("thr_destination"); len(got) != 3 || len(b.under("thr_source")) != 1 || len(b.updates) != 3 {
		t.Fatalf("dest=%v updates=%v", got, b.updates)
	}
	c, _ := control.Load(dir)
	st, _ := state.Load(dir)
	if !c.AwaitingUser || c.BBTarget != "thr_destination" || st.ChildMove == nil || !strings.HasPrefix(st.ChildMove.Transfer, "move_") || st.ChildMove.Candidate != "" {
		t.Fatalf("%+v %+v", c, st.ChildMove)
	}
	// The moved note and the status line say it, in counts.
	m := movedOf(c, st)
	if m == nil || !strings.Contains(m.Text, "3 child thread(s) moved to the new thread.") || m.ChildrenMoved != 3 {
		t.Fatalf("%+v", m)
	}
	var w bytes.Buffer
	printChildMove(&w, childMoveStatus{At: st.ChildMove.At, Moved: 3, Skipped: 1, Listed: true, Done: true, Transfer: true})
	if !strings.Contains(w.String(), "moved to the thread that took over at the transfer of") {
		t.Fatal(w.String())
	}
	// Recover after it finished is idempotent: nothing more is asked of bb.
	// It runs as a person (the old thread no longer owns the loop).
	t.Setenv("BB_THREAD_ID", "")
	code, out = applyWithChildren(t, path, b, nil, "recover", "--id", st.ChildMove.Transfer)
	if code != exitOK || len(b.updates) != 3 || !strings.Contains(out, "already finished") {
		t.Fatal(code, out, b.updates)
	}
}

// --leave-children moves nothing, and recover keeps that choice.
func TestTransferApplyCanLeaveChildThreads(t *testing.T) {
	path, dir := transferApplyFixture(t)
	b := newChildBB(t, 2, 0)
	code, out := applyWithChildren(t, path, b, nil, "apply", "--from", "thr_source", "--thread", "thr_destination", "--leave-children")
	st, _ := state.Load(dir)
	if code != exitOK || len(b.updates) != 0 || st.ChildMove == nil || !st.ChildMove.Left || strings.Contains(out, "Child threads:") {
		t.Fatal(code, out, st.ChildMove)
	}
	t.Setenv("BB_THREAD_ID", "")
	code, out = applyWithChildren(t, path, b, nil, "recover", "--id", st.ChildMove.Transfer)
	if code != exitOK || len(b.updates) != 0 || !strings.Contains(out, "left where they were") {
		t.Fatal(code, out)
	}
	if c, _ := control.Load(dir); movedOf(c, st) != nil && strings.Contains(movedOf(c, st).Text, "child thread") {
		t.Fatal("moved note claims children")
	}
}

// An apply interrupted before the transfer finished moves no child; recover
// finishes the transfer and then the children.
func TestTransferRecoverMovesChildrenAfterAnInterruptedApply(t *testing.T) {
	for _, boundary := range []string{"held", "state", "config"} {
		t.Run(boundary, func(t *testing.T) {
			path, dir := transferApplyFixture(t)
			b := newChildBB(t, 2, 0)
			code, _ := applyWithChildren(t, path, b, func(step string) error {
				if step == boundary {
					return fmt.Errorf("fixture interruption")
				}
				return nil
			}, "apply", "--from", "thr_source", "--thread", "thr_destination")
			c, _ := control.Load(dir)
			if code != exitFailed || c.PendingTransfer == nil || len(b.updates) != 0 {
				t.Fatalf("%d pending=%v updates=%v", code, c.PendingTransfer, b.updates)
			}
			code, out := applyWithChildren(t, path, b, nil, "recover", "--id", c.PendingTransfer.ID)
			if code != exitOK || len(b.under("thr_destination")) != 2 || !strings.Contains(out, "Child threads: 2 moved") {
				t.Fatal(code, out)
			}
		})
	}
}

// A control write after the transfer (a pause, a resume) ends the moves;
// recover continues them. A child bb said it moved but did not is retried,
// not counted.
func TestTransferChildMovesEndOnAControlWriteAndRecover(t *testing.T) {
	path, dir := transferApplyFixture(t)
	b := newChildBB(t, 4, 0)
	b.onUpdate = func(child string) {
		if child == "thr_lane_01" {
			time.Sleep(2 * time.Millisecond)
			control.Update(dir, func(c *control.File) { c.Paused = true })
		}
	}
	b.ignore = map[string]bool{"thr_lane_00": true}
	code, out := applyWithChildren(t, path, b, nil, "apply", "--from", "thr_source", "--thread", "thr_destination")
	st, _ := state.Load(dir)
	m := st.ChildMove
	if code != exitOK || m == nil || m.Moved != 1 || len(m.Remaining) != 3 || m.Done || !strings.Contains(out, "still to move") {
		t.Fatalf("%d %s %+v", code, out, m)
	}
	b.onUpdate, b.ignore = nil, nil
	t.Setenv("BB_THREAD_ID", "")
	code, out = applyWithChildren(t, path, b, nil, "recover", "--id", m.Transfer)
	st, _ = state.Load(dir)
	if code != exitOK || !st.ChildMove.Done || st.ChildMove.Moved != 4 || len(b.under("thr_source")) != 0 {
		t.Fatalf("%d %s %+v", code, out, st.ChildMove)
	}
	if c, _ := control.Load(dir); !c.AwaitingUser || !c.Paused {
		t.Fatal("recover changed a hold")
	}
}

// A recorded move whose destination is not the thread the loop is bound to
// is dropped by recover, and nothing is moved.
func TestTransferChildMoveRecordIsTiedToTheDestination(t *testing.T) {
	path, dir := transferApplyFixture(t)
	b := newChildBB(t, 2, 0)
	code, _ := applyWithChildren(t, path, b, nil, "apply", "--from", "thr_source", "--thread", "thr_destination", "--leave-children")
	if code != exitOK {
		t.Fatal(code)
	}
	st, _ := state.Load(dir)
	id := st.ChildMove.Transfer
	st.ChildMove = &state.ChildMove{From: "thr_source", To: "thr_somewhere_else", Transfer: id, At: time.Now()}
	state.Save(dir, st)
	t.Setenv("BB_THREAD_ID", "")
	code, out := applyWithChildren(t, path, b, nil, "recover", "--id", id)
	st, _ = state.Load(dir)
	if code == exitOK || len(b.updates) != 0 || st.ChildMove != nil || !strings.Contains(out, "no longer matches") {
		t.Fatal(code, out, st.ChildMove)
	}
}

// Preview counts what apply would move and how many children are on a
// provider other than the destination's, with no child IDs or titles.
func TestTransferPreviewCountsChildThreads(t *testing.T) {
	path, dir := transferFixture(t)
	control.Save(dir, control.File{Enabled: true, Ownership: &control.Ownership{ThreadID: "thr_source", Generation: 1}})
	state.Save(dir, state.File{ActiveTarget: state.Primary})
	b := newChildBB(t, 3, 2)
	b.provider["thr_lane_00"], b.provider["thr_lane_01"] = "claude-code", "claude-code"
	_, r, out := runTransfer(t, path, b.run())
	if r.Children == nil || *r.Children != (transferChildrenPreview{Listed: true, WouldMove: 3, OnOtherProvider: 2}) || len(b.updates) != 0 {
		t.Fatalf("%+v", r.Children)
	}
	if strings.Contains(out, "thr_lane_") || !strings.Contains(out, "2 of them run on a provider other than the destination's") {
		t.Fatal(out)
	}
}
