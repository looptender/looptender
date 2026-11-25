package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func testCodexAuthFile(t *testing.T, dir string, exp int64, tok string) {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	pld, err := json.Marshal(map[string]any{"exp": exp})
	if err != nil {
		t.Fatal(err)
	}
	jwt := hdr + "." + base64.RawURLEncoding.EncodeToString(pld) + ".sig"
	if tok != "" {
		jwt = tok
	}
	body, err := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"access_token": jwt,
			"account_id":   "acct_fictional",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAttachLocalAccountStatusReadsFilesWithoutCLICalls(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "catalog")
	cli := filepath.Join(root, "fictional-codex")
	if err := os.WriteFile(cli, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	freshDir := filepath.Join(root, "codex-fresh")
	expiredDir := filepath.Join(root, "codex-expired")
	missingDir := filepath.Join(root, "codex-missing")
	for _, d := range []string{freshDir, expiredDir, missingDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testCodexAuthFile(t, freshDir, now.Add(time.Hour).Unix(), "")
	testCodexAuthFile(t, expiredDir, now.Add(-time.Hour).Unix(), "")

	err = accounts.Update(dir, func(f *accounts.File) error {
		for _, tc := range []struct {
			label, acctDir string
		}{
			{"Codex Fresh", freshDir},
			{"Codex Expired", expiredDir},
			{"Codex Empty", missingDir},
		} {
			e := accounts.NewEntry(*f, accounts.Entry{
				Label:     tc.label,
				Provider:  "codex",
				Mode:      "explicit_directory",
				Directory: tc.acctDir,
				CLI:       cli,
				Workdir:   root,
			}, "thr_example_owner", now, []string{"HOME=" + root})
			f.Entries = append(f.Entries, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := dockFixtureReader(t)
	reader.accountsDir = dir
	reader.now = func() time.Time { return now }
	rep := reader.read(context.Background(), false)
	if rep.Accounts == nil || len(rep.Accounts.Cards) != 3 {
		t.Fatalf("unexpected cards: %+v", rep.Accounts)
	}
	got := []string{rep.Accounts.Cards[0].LocalAuth, rep.Accounts.Cards[1].LocalAuth, rep.Accounts.Cards[2].LocalAuth}
	want := []string{"file_present", "file_expired", "file_missing"}
	for i := range want {
		if rep.Accounts.Cards[i].Auth.State != "unchecked" {
			t.Errorf("card %d Auth.State = %q, want unchecked", i, rep.Accounts.Cards[i].Auth.State)
		}
		if got[i] != want[i] {
			t.Errorf("card %d LocalAuth = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPanelAccountCheckCapabilityGuardsAndExecution(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "thr_example_owner")
	dir, e := accountFixture(t, "claude-code")
	registryDir := t.TempDir()
	reader := &dockReader{accountsDir: dir, directory: registryDir, refresh: 15 * time.Second, now: time.Now}
	c, err := newPanelChanges(context.Background(), "", reader, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	c.quotaEnv = os.Environ()
	var calls int
	c.checkRun = func(_ context.Context, _ string, args, _ []string, _ string) ([]byte, error) {
		calls++
		if args[0] == "--version" {
			return []byte("2.1.318 (Claude Code)"), nil
		}
		return []byte(`{"loggedIn":true,"subscriptionType":"max","email":"fictional@example.com"}`), nil
	}
	s, err := newPanelState(54321, 5*time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.newSession()
	if err != nil {
		t.Fatal(err)
	}
	h := &panelHandler{state: s, guard: newDockGuard(54321), page: newDockServer(54321, s, reader), changes: c}

	// Capability flag only advertised when full changes (--allow-changes) are enabled.
	for _, mode := range []string{"read_only", "controls_only", "allow_changes"} {
		switch mode {
		case "read_only":
			h.changes = nil
		case "controls_only":
			c.controlsOnly = true
			h.changes = c
		case "allow_changes":
			c.controlsOnly = false
			h.changes = c
		}
		w := httptest.NewRecorder()
		h.served(w)(session)
		var served dockServed
		if err := json.Unmarshal(w.Body.Bytes(), &served); err != nil {
			t.Fatal(err)
		}
		if served.AccountCheck != (mode == "allow_changes") {
			t.Fatalf("%s: AccountCheck=%t, want %t", mode, served.AccountCheck, mode == "allow_changes")
		}
	}

	// Guard checks on POST api/v1/account-checks/<id>/run.
	for _, kind := range []string{"origin", "csrf", "controls_only", "wrong_action", "bad_step", "valid"} {
		body := `{"action":"account_check"}`
		step := "run"
		c.controlsOnly = kind == "controls_only"
		if kind == "wrong_action" {
			body = `{"action":"subscription_quota"}`
		}
		if kind == "bad_step" {
			step = "prepare"
		}
		req := httptest.NewRequest(http.MethodPost, s.base()+s.pagePath()+panelAccountCheckPrefix+e.ID+"/"+step, strings.NewReader(body))
		req.Header.Set("Origin", s.base())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set(panelFormHeader, c.prepareValue(session))
		req.AddCookie(&http.Cookie{Name: panelCookieName, Value: session})
		switch kind {
		case "origin":
			req.Header.Set("Origin", "https://example.com")
		case "csrf":
			req.Header.Set(panelFormHeader, "wrong")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if (w.Code == http.StatusOK) != (kind == "valid") {
			t.Fatalf("%s: status=%d body=%s", kind, w.Code, w.Body.String())
		}
	}
	c.controlsOnly = false
	if calls != 2 {
		t.Fatalf("expected 2 CLI calls (--version + auth status), got %d", calls)
	}
	f, err := accounts.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Entries[0].Last == nil || f.Entries[0].Last.Auth != "reported_present" || f.Entries[0].Last.Plan != "max" {
		t.Fatalf("account observation not saved: %+v", f.Entries[0].Last)
	}

	// Mismatched startup environment records context_changed without calling CLI.
	c.quotaEnv = append(append([]string{}, os.Environ()...), "ANTHROPIC_API_KEY=fictional-other")
	beforeCalls := calls
	reply := c.runAccountCheck(e.ID)
	if calls != beforeCalls || reply.Result == nil || reply.Result.Outcome != "refused" || !strings.Contains(reply.Result.Text, "Review the selection") {
		t.Fatalf("unexpected context_changed reply: calls=%d reply=%+v", calls-beforeCalls, reply.Result)
	}
	f, _ = accounts.Load(dir)
	if f.Entries[0].Attempt == nil || f.Entries[0].Attempt.Reason != "context_changed" {
		t.Fatalf("expected context_changed attempt, got %+v", f.Entries[0].Attempt)
	}
}
