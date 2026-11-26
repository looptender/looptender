package conformance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter"
)

func TestAntigravityWorkingStageWaitsForPromptReceipt(t *testing.T) {
	s := antigravitySubject(t)
	r := s.Rig.(turnRig).rig
	gate := filepath.Join(r.dir, "prompt-gate")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Stage(ctx, a, Active) }()
	defer func() { os.Remove(gate); _ = a.Stop(context.Background()) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(filepath.Join(r.dir, "prompt-blocked")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stand-in did not reach prompt gate")
		}
	}
	if n, err := r.running(); err != nil || n != 1 {
		t.Fatalf("running before receipt: %d %v", n, err)
	}
	select {
	case err := <-done:
		t.Fatalf("working stage returned before prompt receipt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if sent, err := r.Sent(); err != nil || len(sent) != 0 {
		t.Fatalf("prompt delivered before gate: %+v %v", sent, err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("working stage did not observe receipt")
	}
	before, err := r.Sent()
	if err != nil || len(before) != 1 || before[0].Prompt != "stand-in staging turn" {
		t.Fatalf("staging receipt: %+v %v", before, err)
	}
	if err := a.Send(ctx, CheckPrompt, adapter.ModeAuto); err == nil {
		t.Fatal("second send accepted while staging turn is working")
	}
	after, err := r.Sent()
	if err != nil || len(after) != 1 {
		t.Fatalf("refused send delivered a prompt: %+v %v", after, err)
	}
}
