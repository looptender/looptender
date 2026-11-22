package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// status shows the child move in counts, never the children's IDs.
func TestStatusShowsTheChildMove(t *testing.T) {
	f := newDoctorFixture(t)
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	m := &state.ChildMove{From: "thr_example_left", To: "thr_example_new", Candidate: "c", At: now, Listed: true, Remaining: []string{"thr_example_child"}, Moved: 14, Failed: 1, Skipped: 1}
	if err := state.Save(f.state, state.File{ChildMove: m}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if status(env{context.Background(), &out, &out}, cfg, now, false, defaultStaleAfter) != exitOK {
		t.Fatal(out.String())
	}
	text := out.String()
	for _, want := range []string{"child threads: 14 of the left conversation's direct children moved", "1 still to move", "1 could not be moved after 3 attempts", "1 left where they were", "none was messaged, retried or stopped"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "thr_example_child") {
		t.Fatal("a child ID was shown")
	}
	out.Reset()
	status(env{context.Background(), &out, &out}, cfg, now, true, defaultStaleAfter)
	// The summary is counts only. (The raw state record under "state" is
	// the private state.json, as for the created threads.)
	var v struct {
		ChildMove json.RawMessage `json:"childMove"`
	}
	var m2 childMoveStatus
	if json.Unmarshal(out.Bytes(), &v) != nil || json.Unmarshal(v.ChildMove, &m2) != nil || m2.Moved != 14 || m2.Pending != 1 || strings.Contains(string(v.ChildMove), "thr_") {
		t.Fatalf("%s", v.ChildMove)
	}
}
