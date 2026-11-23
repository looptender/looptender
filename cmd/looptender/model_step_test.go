package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// model-step keeps its setting in the state directory: the configuration is
// unchanged and still loads strictly. status shows the setting and a step
// in force.
func TestModelStepIsSetOutsideTheConfiguration(t *testing.T) {
	f := newDoctorFixture(t)
	before, _ := os.ReadFile(f.config)
	cmd := func(args ...string) (int, string) {
		var out, errs bytes.Buffer
		code := modelStepCommand(env{context.Background(), &out, &errs}, append([]string{"-c", f.config}, args...))
		return code, out.String()
	}
	if code, out := cmd(); code != exitOK || out != "model step-down: off\n" {
		t.Fatalf("%d %q", code, out)
	}
	for _, bad := range [][]string{{"--models", "only-one"}, {"--models", "a,a"}, {"--models", "a,../b"}, {"--models", "a,b", "--off"}} {
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
	if code, _ := cmd("--off"); code != exitOK {
		t.Fatal("off")
	}
	if s, err := state.LoadModelStepSetting(f.state); s != nil || err != nil {
		t.Fatalf("%+v %v", s, err)
	}
}
