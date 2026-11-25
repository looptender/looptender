//go:build unix

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
)

// The default panel can pause, stop and resume, each after its review, and
// nothing more: no build replacement, route or send-allowance edits,
// account selection review, sign-in or quota reads, which need
// --allow-changes. The page is told which.
func TestTheDefaultPanelPausesStopsAndResumesOnly(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	w := newPanelControlsWorld(t)
	if !w.rec.Controls || w.rec.Changes {
		t.Fatalf("%+v", w.rec)
	}
	_, body := dockRequest(t, http.MethodGet, w.page+dockServerPath, withCookie(w.cookie))
	var served dockServed
	if err := json.Unmarshal([]byte(body), &served); err != nil {
		t.Fatal(err)
	}
	if served.Changes != "on" || served.Form == "" || served.RouteChanges != "" || served.AccountQuota || served.AccountSelectionReview || served.SendAllowanceEdit || len(served.AccountConnectProviders) != 0 ||
		!strings.HasPrefix(served.ChangesText.render(dockNow), "Pause, stop and resume allowed for") || !strings.Contains(served.ChangesText.render(dockNow), "need looptender panel --allow-changes") {
		t.Fatalf("served: %s", body)
	}
	e := w.entry(t, "docs-site-refresh")
	key := dockKey(e.ID)
	// Pause, then resume, each through its review.
	for _, action := range []string{"pause", "resume"} {
		_, p := w.post(t, key, "prepare", w.form, `{"action":"`+action+`"}`, nil)
		if p.Preview == nil {
			t.Fatalf("%s: no review: %+v", action, p)
		}
		_, r := w.post(t, key, "confirm", p.Preview.Form, `{"action":"`+action+`"}`, nil)
		if r.Result == nil || r.Result.Outcome != "done" {
			t.Fatalf("%s: %+v", action, r.Result)
		}
	}
	if c := controlOf(t, e); c.Paused {
		t.Fatalf("not resumed: %+v", c)
	}
	// A build replacement is refused before any review.
	if resp, p := w.post(t, key, "prepare", w.form, `{"action":"replace_build"}`, nil); resp.StatusCode == http.StatusOK || p.Preview != nil {
		t.Fatalf("build replacement offered: %d %+v", resp.StatusCode, p)
	}
	// The other changes' endpoints answer nothing.
	for _, prefix := range []string{panelRoutesPrefix, panelAccountConnectPrefix, panelAccountSelectionPrefix, panelAccountQuotaPrefix, panelAccountRemovePrefix, panelSendAllowancePrefix} {
		req, _ := http.NewRequest(http.MethodPost, w.page+prefix+"x/prepare", strings.NewReader(`{}`))
		req.Header.Set("Origin", w.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(panelFormHeader, w.form)
		req.AddCookie(w.cookie)
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: %d", prefix, resp.StatusCode)
		}
	}
	// The page is never offered the build change.
	_, page := dockRequest(t, http.MethodGet, w.page+dockAPIPath, withCookie(w.cookie))
	if strings.Contains(page, `"action":"replace_build"`) {
		t.Fatal("the default panel offered a build replacement")
	}
}

// The supervised conversation, and anything its supervisor started, cannot
// use the default panel to lift its own loop's hold: the resume is refused
// before any review, as on the command line.
func TestTheDefaultPanelRefusesTheSupervisedSessionsOwnResume(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	w := newPanelControlsWorld(t)
	e := w.entry(t, "docs-site-refresh")
	if _, err := control.Update(e.StateDir, func(f *control.File) { f.Paused = true }); err != nil {
		t.Fatal(err)
	}
	t.Setenv(control.SupervisedLoopVar, e.StateDir)
	_, p := w.post(t, dockKey(e.ID), "prepare", w.form, `{"action":"resume"}`, nil)
	if p.Preview != nil || p.Result == nil || p.Result.Text != panelSupervisedSession+" Nothing was changed." {
		t.Fatalf("%+v", p)
	}
	if !controlOf(t, e).Paused {
		t.Fatal("the hold was lifted")
	}
	// A process its supervisor started is an agent, never a person, for the
	// panel: it does not share a person's panel.
	if panelCallerKind() != "agent" || panelCallerHash() == "" {
		t.Fatalf("%s %q", panelCallerKind(), panelCallerHash())
	}
	if address, err := panelRequestOpening(context.Background(), w.dir, w.rec, "", false); err == nil || address != "" {
		t.Fatalf("a supervised process opened a person's panel: %s %v", address, err)
	}
	// The background panel keeps the marker, so it refuses as the command does.
	found := false
	for _, kv := range panelServerEnvironment() {
		found = found || kv == control.SupervisedLoopVar+"="+e.StateDir
	}
	if !found {
		t.Fatal("the panel's environment drops the supervised-loop marker")
	}
}

// Opening the panel again without a flag turns a running read-only panel
// into the default one, by stopping it and starting one (never two): a page
// open on it ends. --allow-changes strengthens the default one the same way;
// the default reuses a panel that allows every change; --read-only against
// a panel that changes loops is refused.
func TestOpeningThePanelAgainStrengthensItsMode(t *testing.T) {
	w := newPanelProcessWorld(t)
	w.env = append(w.env, "BB_THREAD_ID=")
	if code, out, errs := w.open("--read-only", "--no-open"); code != exitOK || strings.Contains(out, "can pause") {
		t.Fatalf("read-only: %d %s %s", code, out, errs)
	}
	ro := w.record()
	if ro.mode() != panelReadOnly {
		t.Fatalf("%+v", ro)
	}
	code, out, errs := w.open("--no-open")
	if code != exitOK || !strings.Contains(out, "Restarted the running panel") || !strings.Contains(out, "The page can pause, stop and resume loops") {
		t.Fatalf("default: %d %s %s", code, out, errs)
	}
	ctl := w.record()
	if ctl.mode() != panelControlsMode || ctl.Instance == ro.Instance {
		t.Fatalf("%+v", ctl)
	}
	if code, out, errs := w.open("--allow-changes", "--no-open"); code != exitOK || !strings.Contains(out, "Restarted the running panel") {
		t.Fatalf("all changes: %d %s %s", code, out, errs)
	}
	full := w.record()
	if full.mode() != panelFullMode || full.Instance == ctl.Instance {
		t.Fatalf("%+v", full)
	}
	if code, out, errs := w.open("--no-open"); code != exitOK || strings.Contains(out, "Restarted") {
		t.Fatalf("default on a full panel: %d %s %s", code, out, errs)
	}
	if w.record() != full {
		t.Fatal("the default replaced a panel that allows every change")
	}
	if code, _, errs := w.open("--read-only", "--no-open"); code != exitFailed || !strings.Contains(errs, "stop first to open a read-only one") {
		t.Fatalf("read-only on a full panel: %d %s", code, errs)
	}
	if code, _, errs := w.open("--read-only", "--allow-changes", "--no-open"); code != exitUsage {
		t.Fatalf("both flags: %d %s", code, errs)
	}
	if code, out, _ := w.run("panel", "status", "--json", "--panel-directory", w.dir); code != exitOK || !strings.Contains(out, `"mode":"all"`) {
		t.Fatalf("status: %d %s", code, out)
	}
}
