//go:build unix

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusCountsSuccessors(t *testing.T) {
	m := newFakeMaker(2, 2)
	path, dir := replaceFixture(t, m)
	runReplace(t, path, "--model", "example-small-model")
	cfg := loadFixture(t, path)
	var out bytes.Buffer
	status(env{context.Background(), &out, &out}, cfg, time.Now(), false, defaultStaleAfter)
	if !strings.Contains(out.String(), "successor threads: 2 blocked child lane(s) have a successor") || strings.Contains(out.String(), "thr_lane_") {
		t.Fatal(out.String())
	}
	_ = dir
}

// Review P2-3: before any handoff, status and doctor say that a handoff to
// a new-thread fallback will start successor lanes, and how to turn it off;
// opting out removes the notice.
func TestSuccessorPlanIsShownBeforeItActs(t *testing.T) {
	f := newThreadFixture(t)
	_, out, _ := f.run("status")
	want := "successor lanes (on by default): A handoff to claude-other-account starts up to 5 successor thread(s)"
	if !strings.Contains(out, want) || !strings.Contains(out, "bb.replace_blocked_children: false") {
		t.Fatalf("status:\n%s", out)
	}
	_, out, _ = f.run("doctor", "--offline")
	if !strings.Contains(out, "routes.successors.claude-other-account") || !strings.Contains(out, "starts up to 5 successor thread(s)") {
		t.Fatalf("doctor:\n%s", out)
	}
	fallbacks, _ := os.ReadFile(filepath.Join(f.dir, "fallbacks.yaml"))
	write(t, filepath.Join(f.dir, "fallbacks.yaml"), strings.Replace(string(fallbacks), "new_thread: true,", "new_thread: true, replace_blocked_children: false,", 1), 0o600)
	if _, out, _ = f.run("status"); strings.Contains(out, "successor lanes") {
		t.Fatalf("opted out:\n%s", out)
	}
}
