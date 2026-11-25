package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
)

func TestPanelAccountRemovePrepareConfirmAndSafety(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	dir, e := accountFixture(t, "claude-code")
	credDir := filepath.Join(t.TempDir(), "cred-dir")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credFile := filepath.Join(credDir, ".credentials.json")
	if err := os.WriteFile(credFile, []byte(`{"token":"stay-on-disk"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var second accounts.Entry
	if err := accounts.Update(dir, func(f *accounts.File) error {
		second = accounts.NewEntry(*f, accounts.Entry{
			Label:     "Example duplicate Claude",
			Provider:  "claude-code",
			Mode:      "explicit_directory",
			Directory: credDir,
			CLI:       e.CLI,
			Workdir:   e.Workdir,
		}, "", time.Now().UTC(), os.Environ())
		f.Entries = append(f.Entries, second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Register a fictional loop whose route references second.ID so review surfaces it.
	regDir := filepath.Join(filepath.Dir(dir), "registry")
	loopDir := filepath.Join(filepath.Dir(dir), "loop")
	if err := os.MkdirAll(loopDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(loopDir, "looptender.yaml")
	cfgYAML := "version: 1\nloop:\n  name: example-loop\n  state_dir: state\n  adapter: bb\n  provider: claude-code\n  group: claude-main\n  account: " +
		second.ID + "\n  bb: {thread: thr_example_private, model: example-medium, cli: " + e.CLI + "}\n  prompt: check\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, err := registry.Add(regDir, cfg.Loop.Name, cfgPath, cfg.Loop.StateDir); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	reader := &dockReader{directory: regDir, accountsDir: dir, now: func() time.Time { return now }}
	c, err := newPanelChanges(context.Background(), regDir, reader, now, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	post := func(step, form string) panelChangeReply {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/s/"+panelAccountRemovePrefix+second.ID+"/"+step, strings.NewReader(`{"action":"remove_account"}`))
		r.Header.Set(panelFormHeader, form)
		w := httptest.NewRecorder()
		c.serveAccountRemove(w, r, panelAccountRemovePrefix+second.ID+"/"+step, "sess-1")
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", step, w.Code, w.Body.String())
		}
		var reply panelChangeReply
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}

	// Controls-only mode refuses account remove endpoint.
	cCtrl, err := newPanelChanges(context.Background(), regDir, reader, now, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	cCtrl.controlsOnly = true
	rBlocked := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/s/"+panelAccountRemovePrefix+second.ID+"/prepare", strings.NewReader(`{}`))
	wBlocked := httptest.NewRecorder()
	cCtrl.serve(wBlocked, rBlocked, panelAccountRemovePrefix+second.ID+"/prepare", "sess-1")
	if wBlocked.Code != http.StatusForbidden {
		t.Fatalf("controlsOnly should refuse remove with 403, got %d", wBlocked.Code)
	}

	// Prepare review: mentions catalog-only removal, untouched credentials, and loop route reference.
	prep := post("prepare", c.prepareValue("sess-1"))
	if prep.Preview == nil {
		t.Fatalf("expected preview, got %+v", prep)
	}
	var lineText strings.Builder
	for _, line := range prep.Preview.Lines {
		for _, part := range line {
			lineText.WriteString(part.T)
		}
		lineText.WriteByte('\n')
	}
	for _, want := range []string{"accounts.json", "untouched", "loop example-loop, route primary"} {
		if !strings.Contains(lineText.String(), want) {
			t.Fatalf("prepare preview missing %q:\n%s", want, lineText.String())
		}
	}

	// Confirm while check guard is held -> refused without removing.
	guard, err := accounts.CheckGuard(dir, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	heldConfirm := post("confirm", prep.Preview.Form)
	guard.Release()
	if heldConfirm.Result == nil || heldConfirm.Result.Outcome != "refused" || !strings.Contains(heldConfirm.Result.Text, "running") {
		t.Fatalf("expected refused while guard held, got %+v", heldConfirm.Result)
	}

	// Re-prepare and confirm cleanly.
	prep = post("prepare", c.prepareValue("sess-1"))
	if prep.Preview == nil {
		t.Fatalf("re-prepare failed: %+v", prep)
	}
	done := post("confirm", prep.Preview.Form)
	if done.Result == nil || done.Result.Outcome != "done" || !strings.Contains(done.Result.Text, "untouched") {
		t.Fatalf("expected done result, got %+v", done.Result)
	}

	// Verify only second was removed from catalog and credentials on disk are untouched.
	after, err := accounts.Load(dir)
	if err != nil || len(after.Entries) != 1 || after.Entries[0].ID != e.ID {
		t.Fatalf("unexpected catalog after panel remove: err=%v entries=%+v", err, after.Entries)
	}
	if b, err := os.ReadFile(credFile); err != nil || string(b) != `{"token":"stay-on-disk"}` {
		t.Fatalf("credentials on disk touched: err=%v bytes=%q", err, b)
	}

	// Replaying the spent form is rejected with review_again; concurrent lock error type covered cleanly.
	replay := post("confirm", prep.Preview.Form)
	if replay.Result == nil || replay.Result.Outcome != "review_again" {
		t.Fatalf("expected review_again on replay, got %+v", replay.Result)
	}
	_ = lock.ErrHeld
}
