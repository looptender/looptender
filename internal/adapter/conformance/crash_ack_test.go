package conformance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCrashStageWaitsForExactQuitConsumption(t *testing.T) {
	s := subject(t, "claude")
	r := s.Rig.(turnRig).rig
	gate := filepath.Join(r.dir, "consume-gate")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(gate) })
	// A receipt from a different generation/agent must not permit clearing quit.
	if err := os.WriteFile(filepath.Join(r.dir, "quit-ack"), []byte("old-stage 999999"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Stage(context.Background(), a, Crashed) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(filepath.Join(r.dir, "quit-blocked")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stand-in did not reach delayed consumption boundary")
		}
	}
	select {
	case err := <-done:
		t.Fatalf("crash stage returned before matching consumption receipt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(r.dir, "quit")); err != nil {
		t.Fatalf("pending signal disappeared: %v", err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matching consumption did not release staging")
	}
	// The old agent consumed its generation's quit, but that alone grants
	// no identity for a fresh conversation. Explicitly authorize the next
	// fictional staging attempt; the receipt assertions above remain intact.
	if obs, err := a.Observe(context.Background()); err != nil || !obs.SessionLost {
		t.Fatalf("crashed identity: %+v %v", obs, err)
	}
	quitRecords := map[string][]byte{}
	for _, name := range []string{"quit", "quit-ack"} {
		b, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		quitRecords[name] = b
	}
	if string(quitRecords["quit"]) != string(quitRecords["quit-ack"]) {
		t.Fatal("consumed receipt does not match quit")
	}
	resetter, ok := a.(interface{ ResetSession() (bool, error) })
	if !ok {
		t.Fatal("fixture adapter does not support explicit reset")
	}
	if _, err := resetter.ResetSession(); err != nil {
		t.Fatal(err)
	}
	for name, before := range quitRecords {
		after, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil || string(after) != string(before) {
			t.Fatalf("reset changed %s: %q %v", name, after, err)
		}
	}
	if err := r.Stage(context.Background(), a, ExitNonZero); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredCrashReceiptCannotClearOrReuseQuit(t *testing.T) {
	r := &rig{dir: t.TempDir(), pendingQuit: "stage-1 123", pendingDeadline: time.Now().Add(-time.Second)}
	quit := filepath.Join(r.dir, "quit")
	if err := os.WriteFile(quit, []byte(r.pendingQuit), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "quit-ack"), []byte("stage-1 456"), 0600); err != nil {
		t.Fatal(err)
	}
	// The expired staging deadline is retained; a later Stage cannot buy a new
	// timeout or clear the outstanding signal based on another agent's receipt.
	if err := r.Stage(context.Background(), nil, Idle); err == nil {
		t.Fatal("mismatched receipt permitted reuse")
	}
	b, err := os.ReadFile(quit)
	if err != nil || string(b) != r.pendingQuit {
		t.Fatalf("pending quit changed: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "situation")); !os.IsNotExist(err) {
		t.Fatalf("later stage began: %v", err)
	}
}
