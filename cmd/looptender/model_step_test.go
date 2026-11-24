package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// model-step keeps its setting in the state directory: the configuration is
// unchanged and still loads strictly. status shows the setting and a step
// in force.
func TestModelStepIsSetOutsideTheConfiguration(t *testing.T) {
	f := newDoctorFixture(t)
	before, _ := os.ReadFile(f.config)
	t.Setenv("BB_THREAD_ID", "")
	cmd := func(args ...string) (int, string) {
		var out, errs bytes.Buffer
		code := modelStepCommand(env{context.Background(), &out, &errs}, append([]string{"-c", f.config}, args...))
		return code, out.String() + errs.String()
	}
	if code, out := cmd(); code != exitOK || out != "model step-down: off\n" {
		t.Fatalf("%d %q", code, out)
	}
	for _, bad := range [][]string{{"--models", "only-one"}, {"--models", "claude-opus-5-5,claude-opus-4"}, {"--models", "a,../b"}, {"--models", "a,b", "--off"}} {
		if code, _ := cmd(bad...); code != exitUsage {
			t.Fatalf("accepted %v", bad)
		}
	}
	if code, out := cmd("--models", "claude-opus-example, claude-sonnet-example"); code != exitOK || out != "model step-down: on, through claude-opus-example, claude-sonnet-example, at a limit on one model's weekly window\n" {
		t.Fatalf("%d %q", code, out)
	}
	after, _ := os.ReadFile(f.config)
	if !bytes.Equal(before, after) {
		t.Fatal("the configuration changed")
	}
	if _, err := config.Load(f.config); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveModelStep(f.state, &state.ModelStep{From: "claude-opus-example", To: "claude-sonnet-example", Window: "seven_day_opus", ResetsAt: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	printModelStep(&b, describeModelStep(f.state))
	if !strings.Contains(b.String(), "stepped down from claude-opus-example to claude-sonnet-example (seven_day_opus); steps back after 2026-03-06T09:00:00Z") {
		t.Fatal(b.String())
	}
	// Off cancels the pending step-back and says the model stays.
	if code, out := cmd("--off"); code != exitOK || !strings.Contains(out, "the step back is cancelled; the conversation stays on claude-sonnet-example") {
		t.Fatalf("off: %d %q", code, out)
	}
	if rec, _ := state.LoadModelStep(f.state); rec != nil {
		t.Fatal("pending step-back kept")
	}
	if s, err := state.LoadModelStepSetting(f.state); s != nil || err != nil {
		t.Fatalf("%+v %v", s, err)
	}
}

// model-step changes the owner's live conversation: refused from another
// owner's thread and from the supervised conversation itself, as resume is.
func TestModelStepIsTheOwnersToSet(t *testing.T) {
	f := newDoctorFixture(t)
	if _, err := control.Update(f.state, func(c *control.File) {
		c.Ownership = &control.Ownership{ThreadID: "thr_example_owner", Generation: 1}
	}); err != nil {
		t.Fatal(err)
	}
	run := func(caller string) int {
		t.Setenv("BB_THREAD_ID", caller)
		var out, errs bytes.Buffer
		return modelStepCommand(env{context.Background(), &out, &errs}, []string{"-c", f.config, "--models", "claude-opus-example,claude-sonnet-example"})
	}
	for _, caller := range []string{"thr_example_other", "thr_example"} {
		if code := run(caller); code == exitOK {
			t.Fatalf("%s was let set it", caller)
		}
		if s, _ := state.LoadModelStepSetting(f.state); s != nil {
			t.Fatalf("%s set it", caller)
		}
	}
	if code := run("thr_example_owner"); code != exitOK {
		t.Fatal("the owner was refused")
	}
}
