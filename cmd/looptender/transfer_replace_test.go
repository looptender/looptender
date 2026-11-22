package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/supervise"
)

// fakeMaker is bb for transfer replace-children: children of the loop's
// thread, their providers and facts, spawns and title lookups.
type fakeMaker struct {
	children []bb.ListedChild
	provider map[string]string
	facts    map[string]decide.LaneFacts
	titles   map[string][]string
	spawned  []bb.SpawnRequest
	spawnErr error
	unlisted bool
}

func (m *fakeMaker) ListChildren(context.Context, string) ([]bb.ListedChild, error) {
	return m.children, nil
}
func (m *fakeMaker) InfoOf(_ context.Context, id string) (bb.ThreadInfo, error) {
	return bb.ThreadInfo{Place: bb.Place{Project: "proj_fixture", Environment: "env_fixture"}, Provider: m.provider[id]}, nil
}
func (m *fakeMaker) InspectLane(_ context.Context, id string) decide.LaneFacts { return m.facts[id] }
func (m *fakeMaker) FindByTitle(_ context.Context, _ bb.Place, title string) ([]string, error) {
	return m.titles[title], nil
}
func (m *fakeMaker) Spawn(_ context.Context, r bb.SpawnRequest) (string, error) {
	m.spawned = append(m.spawned, r)
	id := fmt.Sprintf("thr_new_lane_%d", len(m.spawned))
	if !m.unlisted {
		m.titles[r.Title] = append(m.titles[r.Title], id)
	}
	if m.spawnErr != nil {
		return "", m.spawnErr
	}
	m.children = append(m.children, bb.ListedChild{ID: id})
	m.provider[id] = r.Provider
	return id, nil
}

func newFakeMaker(lanes, blocked int) *fakeMaker {
	m := &fakeMaker{provider: map[string]string{}, facts: map[string]decide.LaneFacts{}, titles: map[string][]string{}}
	for i := 0; i < lanes; i++ {
		id := fmt.Sprintf("thr_lane_%02d", i)
		m.children = append(m.children, bb.ListedChild{ID: id})
		m.provider[id] = "claude-code"
		m.facts[id] = decide.LaneFacts{Verified: true, InputKnown: true}
		if i < blocked {
			m.facts[id] = decide.LaneFacts{Verified: true, Errored: true, InputKnown: true, ClassKnown: true, Class: decide.ErrRateLimit}
		}
	}
	return m
}

func replaceFixture(t *testing.T, m *fakeMaker) (string, string) {
	t.Helper()
	t.Setenv("BB_THREAD_ID", "")
	path, dir := transferFixture(t)
	control.Save(dir, control.File{Enabled: true, AwaitingUser: true})
	state.Save(dir, state.File{ActiveTarget: state.Primary})
	old := newSuccessorMaker
	newSuccessorMaker = func(cli, coordinator string) supervise.ChildSuccessorMaker {
		if coordinator != "thr_source" {
			t.Fatalf("built for %s", coordinator)
		}
		return m
	}
	t.Cleanup(func() { newSuccessorMaker = old })
	return path, dir
}

func runReplace(t *testing.T, path string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := transferCommand(env{ctx: context.Background(), stdout: &out, stderr: &errs}, append([]string{"replace-children", "-c", path}, args...), nil)
	if strings.Contains(out.String()+errs.String(), "thr_lane_") || strings.Contains(out.String()+errs.String(), "thr_new_lane_") {
		t.Fatalf("a thread ID was printed: %s %s", out.String(), errs.String())
	}
	return code, out.String(), errs.String()
}

// The preview counts and creates nothing; apply creates one successor per
// blocked lane on the loop's provider, parented to the loop's thread, and
// leaves the loop held.
func TestTransferReplaceChildrenPreviewAndApply(t *testing.T) {
	m := newFakeMaker(3, 2)
	path, dir := replaceFixture(t, m)
	code, out, _ := runReplace(t, path, "--dry-run")
	if code != exitOK || len(m.spawned) != 0 || !strings.Contains(out, "Would create 2 successor thread(s): 2 child lane(s) are blocked on a usage limit of another provider") {
		t.Fatal(code, out)
	}
	if code, _, errs := runReplace(t, path); code != exitUsage || !strings.Contains(errs, "--model is required") {
		t.Fatal(code, errs)
	}
	code, out, _ = runReplace(t, path, "--model", "example-small-model", "--json")
	var r struct {
		Outcome  string                  `json:"outcome"`
		Result   supervise.ReplaceResult `json:"result"`
		Lifetime int                     `json:"lifetimeCreated"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &r) != nil || r.Outcome != "applied" || r.Result.Created != 2 || r.Lifetime != 2 || len(m.spawned) != 2 {
		t.Fatal(code, out)
	}
	for i, s := range m.spawned {
		if s.Provider != "codex" || s.Model != "example-small-model" || s.Permission != "accept-edits" || s.Parent != "thr_source" || s.Project != "proj_fixture" ||
			!strings.HasSuffix(s.Prompt, fmt.Sprintf("Continue from @thread:thr_lane_%02d.\n", i)) {
			t.Fatalf("%+v", s)
		}
	}
	st, _ := state.Load(dir)
	c, _ := control.Load(dir)
	if st.ChildSuccessorsCreated != 2 || !st.Replaced("thr_lane_00") || !c.AwaitingUser || c.Paused {
		t.Fatalf("%+v %+v", st.ChildSuccessors, c)
	}
	// Run again: nothing more to replace.
	if code, out, _ := runReplace(t, path, "--model", "example-small-model"); code != exitOK || len(m.spawned) != 2 || !strings.Contains(out, "Created 0") {
		t.Fatal(code, out)
	}
	// --all-old-provider widens it to the remaining lane on the old provider.
	if code, out, _ := runReplace(t, path, "--model", "example-small-model", "--all-old-provider"); code != exitOK || len(m.spawned) != 3 {
		t.Fatal(code, out)
	}
}

// A successor that cannot be confirmed stops the run with its title; the
// next run settles it by title and creates nothing twice.
func TestTransferReplaceChildrenUnsettled(t *testing.T) {
	m := newFakeMaker(2, 2)
	path, dir := replaceFixture(t, m)
	m.spawnErr, m.unlisted = bb.ErrSpawnUncertain, true
	code, _, errs := runReplace(t, path, "--model", "example-small-model")
	st, _ := state.Load(dir)
	if code != exitFailed || len(m.spawned) != 1 || st.ChildSuccessors.Intent == nil || !strings.Contains(errs, "lane (continued, ") {
		t.Fatal(code, errs, st.ChildSuccessors)
	}
	m.titles[st.ChildSuccessors.Intent.Title] = []string{"thr_found_lane"}
	m.spawnErr, m.unlisted = nil, false
	code, _, errs = runReplace(t, path, "--model", "example-small-model")
	st, _ = state.Load(dir)
	if code != exitOK || len(m.spawned) != 2 || st.ChildSuccessors.Replaced["thr_lane_00"].Thread != "thr_found_lane" || st.ChildSuccessorsCreated != 2 {
		t.Fatal(code, errs, st.ChildSuccessors)
	}
}

// Refused while a transfer is pending or the loop is superseded.
func TestTransferReplaceChildrenRefusals(t *testing.T) {
	m := newFakeMaker(1, 1)
	path, dir := replaceFixture(t, m)
	control.Update(dir, func(c *control.File) {
		c.PendingTransfer = &control.PendingBBTransfer{Version: 1, ID: "move_" + strings.Repeat("e", 32), From: "thr_source", To: "thr_destination",
			Before: strings.Repeat("1", 64), After: strings.Repeat("2", 64)}
	})
	if code, _, errs := runReplace(t, path, "--model", "m"); code != exitFailed || !strings.Contains(errs, "transfer is pending") || len(m.spawned) != 0 {
		t.Fatal(code, errs)
	}
	control.Update(dir, func(c *control.File) { c.PendingTransfer, c.SupersededBy = nil, "thr_destination" })
	if code, _, _ := runReplace(t, path, "--model", "m"); code != exitFailed || len(m.spawned) != 0 {
		t.Fatal(code)
	}
}

// Review P2-2: a person can discard a successor creation that could not be
// confirmed; it stays counted, and the next run carries on.
func TestDiscardPendingSuccessor(t *testing.T) {
	m := newFakeMaker(2, 2)
	path, dir := replaceFixture(t, m)
	if code, _, errs := runReplace(t, path, "--discard-pending-successor"); code != exitFailed || !strings.Contains(errs, "no successor creation is pending") {
		t.Fatal(code, errs)
	}
	m.spawnErr, m.unlisted = bb.ErrSpawnUncertain, true
	runReplace(t, path, "--model", "example-small-model")
	m.spawnErr, m.unlisted = nil, false
	if code, _, _ := runReplace(t, path, "--model", "example-small-model"); code != exitFailed {
		t.Fatal("not still pending")
	}
	code, out, _ := runReplace(t, path, "--discard-pending-successor")
	st, _ := state.Load(dir)
	if code != exitOK || st.ChildSuccessors.Intent != nil || st.ChildSuccessorsCreated != 1 || !strings.Contains(out, "stop or archive it now") {
		t.Fatal(code, out, st.ChildSuccessors)
	}
	if code, _, errs := runReplace(t, path, "--discard-pending-successor", "--model", "x"); code != exitUsage {
		t.Fatal(code, errs)
	}
	if code, _, _ := runReplace(t, path, "--model", "example-small-model"); code != exitOK || len(m.spawned) != 3 {
		t.Fatal(code, len(m.spawned))
	}
}
